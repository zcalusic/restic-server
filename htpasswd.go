package restserver

/*
Original version copied from: github.com/bitly/oauth2_proxy

MIT License

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in
all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
THE SOFTWARE.
*/

import (
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/csv"
	"log"
	"os"
	"os/signal"
	"regexp"
	"sync"
	"syscall"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// CheckInterval represents how often we check for changes in htpasswd file.
const CheckInterval = 30 * time.Second

// PasswordCacheDuration represents how long authentication credentials are
// cached in memory after they were successfully verified. This allows avoiding
// repeatedly verifying the same authentication credentials.
const PasswordCacheDuration = time.Minute

// Lookup passwords in a htpasswd file.  The entries must have been created with -s for SHA encryption.

type cacheEntry struct {
	expiry   time.Time
	verifier []byte
}

// HtpasswdFile is a map for usernames to passwords.
type HtpasswdFile struct {
	mutex    sync.Mutex
	path     string
	stat     os.FileInfo
	throttle chan struct{}
	users    map[string]string
	cache    map[string]cacheEntry
}

// NewHtpasswdFromFile reads the users and passwords from a htpasswd file and returns them.  If an error is encountered,
// it is returned, together with a nil-Pointer for the HtpasswdFile.
func NewHtpasswdFromFile(path string) (*HtpasswdFile, error) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGHUP)
	stat, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	h := &HtpasswdFile{
		mutex:    sync.Mutex{},
		path:     path,
		stat:     stat,
		throttle: make(chan struct{}),
		cache:    make(map[string]cacheEntry),
	}

	if err := h.Reload(); err != nil {
		return nil, err
	}

	// Start a goroutine that limits reload checks to once per CheckInterval
	go h.throttleTimer()
	go h.expiryTimer()

	go func() {
		for range c {
			err := h.Reload()
			if err == nil {
				log.Printf("Reloaded htpasswd file")
			} else {
				log.Printf("Could not reload htpasswd file: %v", err)
			}
		}
	}()

	return h, nil
}

// throttleTimer sends at most one message per CheckInterval to throttle file change checks.
func (h *HtpasswdFile) throttleTimer() {
	var check struct{}
	for {
		time.Sleep(CheckInterval)
		h.throttle <- check
	}
}

func (h *HtpasswdFile) expiryTimer() {
	for {
		time.Sleep(5 * time.Second)
		now := time.Now()
		h.mutex.Lock()
		var zeros [sha256.Size]byte
		// try to wipe expired cache entries
		for user, entry := range h.cache {
			if entry.expiry.After(now) {
				copy(entry.verifier, zeros[:])
				delete(h.cache, user)
			}
		}
		h.mutex.Unlock()
	}
}

var validUsernameRegexp = regexp.MustCompile(`^[\p{L}\d@.-]+$`)

// Reload reloads the htpasswd file. If the reload fails, the Users map is not changed and the error is returned.
func (h *HtpasswdFile) Reload() error {
	r, err := os.Open(h.path)
	if err != nil {
		return err
	}

	cr := csv.NewReader(r)
	cr.Comma = ':'
	cr.Comment = '#'
	cr.TrimLeadingSpace = true

	records, err := cr.ReadAll()
	if err != nil {
		_ = r.Close()
		return err
	}
	users := make(map[string]string)
	for _, record := range records {
		if !validUsernameRegexp.MatchString(record[0]) {
			log.Printf("Ignoring invalid username %q in htpasswd, consists of characters other than letters", record[0])
			continue
		}
		users[record[0]] = record[1]
	}

	// Replace the Users map
	h.mutex.Lock()
	var zeros [sha256.Size]byte
	// try to wipe the old cache entries
	for _, entry := range h.cache {
		copy(entry.verifier, zeros[:])
	}
	h.cache = make(map[string]cacheEntry)

	h.users = users
	h.mutex.Unlock()

	_ = r.Close()
	return nil
}

// ReloadCheck checks at most once per CheckInterval if the file changed and will reload the file if it did.
// It logs errors and successful reloads, and returns an error if any was encountered.
func (h *HtpasswdFile) ReloadCheck() error {
	select {
	case <-h.throttle:
		stat, err := os.Stat(h.path)
		if err != nil {
			log.Printf("Could not stat htpasswd file: %v", err)
			return err
		}

		reload := false

		h.mutex.Lock()
		if stat.ModTime() != h.stat.ModTime() || stat.Size() != h.stat.Size() {
			reload = true
			h.stat = stat
		}
		h.mutex.Unlock()

		if reload {
			err := h.Reload()
			if err == nil {
				log.Printf("Reloaded htpasswd file")
			} else {
				log.Printf("Could not reload htpasswd file: %v", err)
				return err
			}
		}
	default:
		// No need to check
	}
	return nil
}

// Validate returns true if password matches the stored password for user.  If no password for user is stored, or the
// password is wrong, false is returned.
func (h *HtpasswdFile) Validate(user string, password string) bool {
	_ = h.ReloadCheck()

	hash := sha256.New()
	// hash.Write can never fail
	_, _ = hash.Write([]byte(user))
	_, _ = hash.Write([]byte(":"))
	_, _ = hash.Write([]byte(password))

	h.mutex.Lock()
	// avoid race conditions with cache replacements
	cache := h.cache
	realPassword, exists := h.users[user]
	entry, cacheExists := h.cache[user]
	h.mutex.Unlock()

	if !exists {
		return false
	}

	if cacheExists && subtle.ConstantTimeCompare(entry.verifier, hash.Sum(nil)) == 1 {
		h.mutex.Lock()
		// repurpose mutex to prevent concurrent cache updates
		// extend cache entry
		cache[user] = cacheEntry{
			verifier: entry.verifier,
			expiry:   time.Now().Add(PasswordCacheDuration),
		}
		h.mutex.Unlock()
		return true
	}

	var shaRe = regexp.MustCompile(`^{SHA}`)
	var bcrRe = regexp.MustCompile(`^\$2b\$|^\$2a\$|^\$2y\$`)

	isValid := false

	switch {
	case shaRe.MatchString(realPassword):
		d := sha1.New()
		_, _ = d.Write([]byte(password))
		if realPassword[5:] == base64.StdEncoding.EncodeToString(d.Sum(nil)) {
			isValid = true
		}
	case bcrRe.MatchString(realPassword):
		err := bcrypt.CompareHashAndPassword([]byte(realPassword), []byte(password))
		if err == nil {
			isValid = true
		}
	}

	if !isValid {
		log.Printf("Invalid htpasswd entry for %s.", user)
		return false
	}

	h.mutex.Lock()
	// repurpose mutex to prevent concurrent cache updates
	cache[user] = cacheEntry{
		verifier: hash.Sum(nil),
		expiry:   time.Now().Add(PasswordCacheDuration),
	}
	h.mutex.Unlock()

	return true
}
