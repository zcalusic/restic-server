package restserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/miolini/datacounter"
	"github.com/prometheus/client_golang/prometheus"
	"goji.io/middleware"
	"goji.io/pat"
)

// Server determines how a Mux's handlers behave.
type Server struct {
	Path         string
	Listen       string
	Log          string
	CPUProfile   string
	TLSKey       string
	TLSCert      string
	TLS          bool
	NoAuth       bool
	AppendOnly   bool
	PrivateRepos bool
	Prometheus   bool
	Debug        bool
	MaxRepoSize  int64

	repoSize int64 // must be accessed using sync/atomic
}

func (s *Server) isHashed(dir string) bool {
	return dir == "data"
}

func valid(name string) bool {
	// taken from net/http.Dir
	if strings.Contains(name, "\x00") {
		return false
	}

	if filepath.Separator != '/' && strings.ContainsRune(name, filepath.Separator) {
		return false
	}

	return true
}

var validTypes = []string{"data", "index", "keys", "locks", "snapshots", "config"}

func (s *Server) isValidType(name string) bool {
	for _, tpe := range validTypes {
		if name == tpe {
			return true
		}
	}

	return false
}

// join takes a number of path names, sanitizes them, and returns them joined
// with base for the current operating system to use (dirs separated by
// filepath.Separator). The returned path is always either equal to base or a
// subdir of base.
func join(base string, names ...string) (string, error) {
	clean := make([]string, 0, len(names)+1)
	clean = append(clean, base)

	// taken from net/http.Dir
	for _, name := range names {
		if !valid(name) {
			return "", errors.New("invalid character in path")
		}

		clean = append(clean, filepath.FromSlash(path.Clean("/"+name)))
	}

	return filepath.Join(clean...), nil
}

// getRepo returns the repository location, relative to s.Path.
func (s *Server) getRepo(r *http.Request) string {
	if strings.HasPrefix(fmt.Sprintf("%s", middleware.Pattern(r.Context())), "/:repo") {
		return pat.Param(r, "repo")
	}

	return "."
}

// getPath returns the path for a file type in the repo.
func (s *Server) getPath(r *http.Request, fileType string) (string, error) {
	if !s.isValidType(fileType) {
		return "", errors.New("invalid file type")
	}
	return join(s.Path, s.getRepo(r), fileType)
}

// getFilePath returns the path for a file in the repo.
func (s *Server) getFilePath(r *http.Request, fileType, name string) (string, error) {
	if !s.isValidType(fileType) {
		return "", errors.New("invalid file type")
	}

	if s.isHashed(fileType) {
		if len(name) < 2 {
			return "", errors.New("file name is too short")
		}

		return join(s.Path, s.getRepo(r), fileType, name[:2], name)
	}

	return join(s.Path, s.getRepo(r), fileType, name)
}

// getUser returns the username from the request, or an empty string if none.
func (s *Server) getUser(r *http.Request) string {
	username, _, ok := r.BasicAuth()
	if !ok {
		return ""
	}
	return username
}

// getMetricLabels returns the prometheus labels from the request.
func (s *Server) getMetricLabels(r *http.Request) prometheus.Labels {
	labels := prometheus.Labels{
		"user": s.getUser(r),
		"repo": s.getRepo(r),
		"type": pat.Param(r, "type"),
	}
	return labels
}

// isUserPath checks if a request path is accessible by the user when using
// private repositories.
func isUserPath(username, path string) bool {
	prefix := "/" + username
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	return len(path) == len(prefix) || path[len(prefix)] == '/'
}

// AuthHandler wraps h with a http.HandlerFunc that performs basic authentication against the user/passwords pairs
// stored in f and returns the http.HandlerFunc.
func (s *Server) AuthHandler(f *HtpasswdFile, h http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok || !f.Validate(username, password) {
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}
		if s.PrivateRepos && !isUserPath(username, r.URL.Path) && r.URL.Path != "/metrics" {
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	}
}

// CheckConfig checks whether a configuration exists.
func (s *Server) CheckConfig(w http.ResponseWriter, r *http.Request) {
	if s.Debug {
		log.Println("CheckConfig()")
	}
	cfg, err := s.getPath(r, "config")
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	st, err := os.Stat(cfg)
	if err != nil {
		if s.Debug {
			log.Print(err)
		}
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		return
	}

	w.Header().Add("Content-Length", fmt.Sprint(st.Size()))
}

// GetConfig allows for a config to be retrieved.
func (s *Server) GetConfig(w http.ResponseWriter, r *http.Request) {
	if s.Debug {
		log.Println("GetConfig()")
	}
	cfg, err := s.getPath(r, "config")
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	bytes, err := ioutil.ReadFile(cfg)
	if err != nil {
		if s.Debug {
			log.Print(err)
		}
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		return
	}

	_, _ = w.Write(bytes)
}

// SaveConfig allows for a config to be saved.
func (s *Server) SaveConfig(w http.ResponseWriter, r *http.Request) {
	if s.Debug {
		log.Println("SaveConfig()")
	}
	cfg, err := s.getPath(r, "config")
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	f, err := os.OpenFile(cfg, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0600)
	if err != nil && os.IsExist(err) {
		if s.Debug {
			log.Print(err)
		}
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return
	}

	_, err = io.Copy(f, r.Body)
	if err != nil {
		if s.Debug {
			log.Print(err)
		}
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	err = f.Close()
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	_ = r.Body.Close()
}

// DeleteConfig removes a config.
func (s *Server) DeleteConfig(w http.ResponseWriter, r *http.Request) {
	if s.Debug {
		log.Println("DeleteConfig()")
	}

	if s.AppendOnly {
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return
	}

	cfg, err := s.getPath(r, "config")
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	if err := os.Remove(cfg); err != nil {
		if s.Debug {
			log.Print(err)
		}
		if os.IsNotExist(err) {
			http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		} else {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		}
		return
	}
}

const (
	mimeTypeAPIV1 = "application/vnd.x.restic.rest.v1"
	mimeTypeAPIV2 = "application/vnd.x.restic.rest.v2"
)

// ListBlobs lists all blobs of a given type in an arbitrary order.
func (s *Server) ListBlobs(w http.ResponseWriter, r *http.Request) {
	if s.Debug {
		log.Println("ListBlobs()")
	}

	switch r.Header.Get("Accept") {
	case mimeTypeAPIV2:
		s.ListBlobsV2(w, r)
	default:
		s.ListBlobsV1(w, r)
	}
}

// ListBlobsV1 lists all blobs of a given type in an arbitrary order.
func (s *Server) ListBlobsV1(w http.ResponseWriter, r *http.Request) {
	if s.Debug {
		log.Println("ListBlobsV1()")
	}
	fileType := pat.Param(r, "type")
	path, err := s.getPath(r, fileType)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	items, err := ioutil.ReadDir(path)
	if err != nil {
		if s.Debug {
			log.Print(err)
		}
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		return
	}

	var names []string
	for _, i := range items {
		if s.isHashed(fileType) {
			subpath := filepath.Join(path, i.Name())
			var subitems []os.FileInfo
			subitems, err = ioutil.ReadDir(subpath)
			if err != nil {
				if s.Debug {
					log.Print(err)
				}
				http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
				return
			}
			for _, f := range subitems {
				names = append(names, f.Name())
			}
		} else {
			names = append(names, i.Name())
		}
	}

	data, err := json.Marshal(names)
	if err != nil {
		if s.Debug {
			log.Print(err)
		}
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", mimeTypeAPIV1)
	_, _ = w.Write(data)
}

// Blob represents a single blob, its name and its size.
type Blob struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// ListBlobsV2 lists all blobs of a given type, together with their sizes, in an arbitrary order.
func (s *Server) ListBlobsV2(w http.ResponseWriter, r *http.Request) {
	if s.Debug {
		log.Println("ListBlobsV2()")
	}
	fileType := pat.Param(r, "type")
	path, err := s.getPath(r, fileType)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	items, err := ioutil.ReadDir(path)
	if err != nil {
		if s.Debug {
			log.Print(err)
		}
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		return
	}

	var blobs []Blob
	for _, i := range items {
		if s.isHashed(fileType) {
			subpath := filepath.Join(path, i.Name())
			var subitems []os.FileInfo
			subitems, err = ioutil.ReadDir(subpath)
			if err != nil {
				if s.Debug {
					log.Print(err)
				}
				http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
				return
			}
			for _, f := range subitems {
				blobs = append(blobs, Blob{Name: f.Name(), Size: f.Size()})
			}
		} else {
			blobs = append(blobs, Blob{Name: i.Name(), Size: i.Size()})
		}
	}

	data, err := json.Marshal(blobs)
	if err != nil {
		if s.Debug {
			log.Print(err)
		}
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", mimeTypeAPIV2)
	_, _ = w.Write(data)
}

// CheckBlob tests whether a blob exists.
func (s *Server) CheckBlob(w http.ResponseWriter, r *http.Request) {
	if s.Debug {
		log.Println("CheckBlob()")
	}

	path, err := s.getFilePath(r, pat.Param(r, "type"), pat.Param(r, "name"))
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	st, err := os.Stat(path)
	if err != nil {
		if s.Debug {
			log.Print(err)
		}
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		return
	}

	w.Header().Add("Content-Length", fmt.Sprint(st.Size()))
}

// GetBlob retrieves a blob from the repository.
func (s *Server) GetBlob(w http.ResponseWriter, r *http.Request) {
	if s.Debug {
		log.Println("GetBlob()")
	}

	path, err := s.getFilePath(r, pat.Param(r, "type"), pat.Param(r, "name"))
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	file, err := os.Open(path)
	if err != nil {
		if s.Debug {
			log.Print(err)
		}
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		return
	}

	wc := datacounter.NewResponseWriterCounter(w)
	http.ServeContent(wc, r, "", time.Unix(0, 0), file)

	if err = file.Close(); err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	if s.Prometheus {
		labels := s.getMetricLabels(r)
		metricBlobReadTotal.With(labels).Inc()
		metricBlobReadBytesTotal.With(labels).Add(float64(wc.Count()))
	}
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

// SaveBlob saves a blob to the repository.
func (s *Server) SaveBlob(w http.ResponseWriter, r *http.Request) {
	if s.Debug {
		log.Println("SaveBlob()")
	}

	path, err := s.getFilePath(r, pat.Param(r, "type"), pat.Param(r, "name"))
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	tf, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0600)
	if os.IsNotExist(err) {
		// the error is caused by a missing directory, create it and retry
		mkdirErr := os.MkdirAll(filepath.Dir(path), 0700)
		if mkdirErr != nil {
			log.Print(mkdirErr)
		} else {
			// try again
			tf, err = os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0600)
		}
	}
	if os.IsExist(err) {
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return
	}
	if err != nil {
		if s.Debug {
			log.Print(err)
		}
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	// ensure this blob does not put us over the repo size limit (if there is one)
	var outFile io.Writer = tf
	if s.MaxRepoSize != 0 {
		var errCode int
		outFile, errCode, err = s.maxSizeWriter(r, tf)
		if err != nil {
			if s.Debug {
				log.Println(err)
			}
			if errCode > 0 {
				http.Error(w, http.StatusText(errCode), errCode)
			}
			return
		}
	}

	written, err := io.Copy(outFile, r.Body)
	if err != nil {
		_ = tf.Close()
		_ = os.Remove(path)
		if s.MaxRepoSize > 0 {
			s.incrementRepoSpaceUsage(-written)
		}
		if s.Debug {
			log.Print(err)
		}
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}

	if err := tf.Sync(); err != nil {
		_ = tf.Close()
		_ = os.Remove(path)
		if s.MaxRepoSize > 0 {
			s.incrementRepoSpaceUsage(-written)
		}
		if s.Debug {
			log.Print(err)
		}
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	if err := tf.Close(); err != nil {
		_ = os.Remove(path)
		if s.MaxRepoSize > 0 {
			s.incrementRepoSpaceUsage(-written)
		}
		if s.Debug {
			log.Print(err)
		}
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	if s.Prometheus {
		labels := s.getMetricLabels(r)
		metricBlobWriteTotal.With(labels).Inc()
		metricBlobWriteBytesTotal.With(labels).Add(float64(written))
	}
}

// DeleteBlob deletes a blob from the repository.
func (s *Server) DeleteBlob(w http.ResponseWriter, r *http.Request) {
	if s.Debug {
		log.Println("DeleteBlob()")
	}

	if s.AppendOnly && pat.Param(r, "type") != "locks" {
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return
	}

	path, err := s.getFilePath(r, pat.Param(r, "type"), pat.Param(r, "name"))
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	var size int64
	if s.Prometheus || s.MaxRepoSize > 0 {
		stat, err := os.Stat(path)
		if err == nil {
			size = stat.Size()
		}
	}

	if err := os.Remove(path); err != nil {
		if s.Debug {
			log.Print(err)
		}
		if os.IsNotExist(err) {
			http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		} else {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		}
		return
	}

	if s.MaxRepoSize > 0 {
		s.incrementRepoSpaceUsage(-size)
	}
	if s.Prometheus {
		labels := s.getMetricLabels(r)
		metricBlobDeleteTotal.With(labels).Inc()
		metricBlobDeleteBytesTotal.With(labels).Add(float64(size))
	}
}

// CreateRepo creates repository directories.
func (s *Server) CreateRepo(w http.ResponseWriter, r *http.Request) {
	if s.Debug {
		log.Println("CreateRepo()")
	}

	repo, err := join(s.Path, s.getRepo(r))
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	if r.URL.Query().Get("create") != "true" {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}

	log.Printf("Creating repository directories in %s\n", repo)

	if err := os.MkdirAll(repo, 0700); err != nil {
		log.Print(err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	for _, d := range validTypes {
		if d == "config" {
			continue
		}

		if err := os.MkdirAll(filepath.Join(repo, d), 0700); err != nil {
			log.Print(err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
	}

	for i := 0; i < 256; i++ {
		if err := os.MkdirAll(filepath.Join(repo, "data", fmt.Sprintf("%02x", i)), 0700); err != nil {
			log.Print(err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
	}
}
