package quota

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
)

// New creates a new quota Manager for given path.
// It will tally the current disk usage before returning.
func New(path string, maxSize int64) (*Manager, error) {
	m := &Manager{
		path:        path,
		maxRepoSize: maxSize,
	}
	if err := m.updateSize(); err != nil {
		return nil, err
	}
	return m, nil
}

// Manager manages the repo quota for given filesystem root path, including subrepos
type Manager struct {
	path        string
	maxRepoSize int64
	repoSize    int64 // must be accessed using sync/atomic
}

// maxSizeWriter limits the number of bytes written
// to the space that is currently available as given by
// the server's MaxRepoSize. This type is safe for use
// by multiple goroutines sharing the same *Server.
type maxSizeWriter struct {
	io.Writer
	m *Manager
}

func (w maxSizeWriter) Write(p []byte) (n int, err error) {
	if int64(len(p)) > w.m.repoSpaceRemaining() {
		return 0, fmt.Errorf("repository has reached maximum size (%d bytes)", w.m.maxRepoSize)
	}
	n, err = w.Writer.Write(p)
	w.m.incrementRepoSpaceUsage(int64(n))
	return n, err
}

func (m *Manager) updateSize() error {
	// if we haven't yet computed the size of the repo, do so now
	initialSize, err := tallySize(m.path)
	if err != nil {
		return err
	}
	atomic.StoreInt64(&m.repoSize, initialSize)
	return nil
}

// maxSizeWriter wraps w in a writer that enforces s.MaxRepoSize.
// If there is an error, a status code and the error are returned.
func (m *Manager) maxSizeWriter(req *http.Request, w io.Writer) (io.Writer, int, error) {
	currentSize := atomic.LoadInt64(&m.repoSize)

	// if content-length is set and is trustworthy, we can save some time
	// and issue a polite error if it declares a size that's too big; since
	// we expect the vast majority of clients will be honest, so this check
	// can only help save time
	if contentLenStr := req.Header.Get("Content-Length"); contentLenStr != "" {
		contentLen, err := strconv.ParseInt(contentLenStr, 10, 64)
		if err != nil {
			return nil, http.StatusLengthRequired, err
		}
		if currentSize+contentLen > m.maxRepoSize {
			err := fmt.Errorf("incoming blob (%d bytes) would exceed maximum size of repository (%d bytes)",
				contentLen, m.maxRepoSize)
			return nil, http.StatusRequestEntityTooLarge, err
		}
	}

	// since we can't always trust content-length, we will wrap the writer
	// in a custom writer that enforces the size limit during writes
	return maxSizeWriter{Writer: w, m: m}, 0, nil
}

// repoSpaceRemaining returns how much space is available in the repo
// according to s.MaxRepoSize. s.repoSize must already be set.
// If there is no limit, -1 is returned.
func (m *Manager) repoSpaceRemaining() int64 {
	if m.maxRepoSize == 0 {
		return -1
	}
	maxSize := m.maxRepoSize
	currentSize := atomic.LoadInt64(&m.repoSize)
	return maxSize - currentSize
}

// incrementRepoSpaceUsage increments the current repo size (which
// must already be initialized).
func (m *Manager) incrementRepoSpaceUsage(by int64) {
	atomic.AddInt64(&m.repoSize, by)
}

// tallySize counts the size of the contents of path.
func tallySize(path string) (int64, error) {
	if path == "" {
		path = "."
	}
	var size int64
	err := filepath.Walk(path, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		size += info.Size()
		return nil
	})
	return size, err
}
