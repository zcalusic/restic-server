package restserver

import (
	"errors"
	"log"
	"net/http"
	"path"
	"path/filepath"
	"strings"

	"github.com/restic/rest-server/config"
	"github.com/restic/rest-server/quota"
	"github.com/restic/rest-server/repo"
)

// NewServer creates a new Server from a config.Config
func NewServer(c config.Config) (*Server, error) {
	s := &Server{
		Path:             c.Path,
		Log:              c.AccessLog,
		NoAuth:           c.Auth.Disabled,
		AppendOnly:       c.AppendOnly,
		PrivateRepos:     c.PrivateRepos,
		Prometheus:       c.Metrics.Enabled,
		PrometheusNoAuth: c.Metrics.NoAuth,
		Debug:            c.Debug,
		MaxRepoSize:      int64(c.Quota.MaxSize),

		Config: c,
	}
	return s, nil
}

// Server encapsulates the rest-server's settings and repo management logic
type Server struct {
	// Old attributes
	// TODO: Remove these before 1.0 and directly use Config instead
	Path             string
	Log              string
	NoAuth           bool
	AppendOnly       bool
	PrivateRepos     bool
	Prometheus       bool
	PrometheusNoAuth bool
	Debug            bool
	MaxRepoSize      int64

	PanicOnError bool
	Config       config.Config

	htpasswdFile *HtpasswdFile
	quotaManager *quota.Manager
}

// MaxFolderDepth is the maxDepth param passed to splitURLPath.
// A max depth of 2 mean that we accept folders like: '/', '/foo' and '/foo/bar'
// TODO: Move to a Server option
const MaxFolderDepth = 2

// httpDefaultError write a HTTP error with the default description
func httpDefaultError(w http.ResponseWriter, code int) {
	http.Error(w, http.StatusText(code), code)
}

// ServeHTTP makes this server an http.Handler. It handlers the administrative
// part of the request (figuring out the filesystem location, performing
// authentication, etc) and then passes it on to repo.Handler for actual
// REST API processing.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// First of all, check auth (will always pass if NoAuth is set)
	username, ok := s.checkAuth(r)
	if !ok {
		httpDefaultError(w, http.StatusUnauthorized)
		return
	}

	// Perform the path parsing to determine the repo folder and remainder for the
	// repo handler.
	folderPath, remainder := splitURLPath(r.URL.Path, MaxFolderDepth)
	if !folderPathValid(folderPath) {
		log.Printf("Invalid request path: %s", r.URL.Path)
		httpDefaultError(w, http.StatusNotFound)
		return
	}

	// Check if the current user is allowed to access this path
	if !s.NoAuth && s.PrivateRepos {
		if len(folderPath) == 0 || folderPath[0] != username {
			httpDefaultError(w, http.StatusUnauthorized)
			return
		}
	}

	// Determine filesystem path for this repo
	fsPath, err := join(s.Path, folderPath...)
	if err != nil {
		// We did not expect an error at this stage, because we just checked the path
		log.Printf("Unexpected join error for path %q", r.URL.Path)
		httpDefaultError(w, http.StatusNotFound)
		return
	}

	// Pass the request to the repo.Handler
	opt := repo.Options{
		AppendOnly:   s.AppendOnly,
		Debug:        s.Debug,
		QuotaManager: s.quotaManager, // may be nil
		PanicOnError: s.PanicOnError,
	}
	if s.Prometheus {
		opt.BlobMetricFunc = makeBlobMetricFunc(username, folderPath)
	}
	repoHandler, err := repo.New(fsPath, opt)
	if err != nil {
		log.Printf("repo.New error: %v", err)
		httpDefaultError(w, http.StatusInternalServerError)
		return
	}
	r.URL.Path = remainder // strip folderPath for next handler
	repoHandler.ServeHTTP(w, r)
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

func isValidType(name string) bool {
	for _, tpe := range repo.ObjectTypes {
		if name == tpe {
			return true
		}
	}
	for _, tpe := range repo.FileTypes {
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

// splitURLPath splits the URL path into a folderPath of the subrepo, and
// a remainder that can be passed to repo.Handler.
// Example: /foo/bar/locks/0123... will be split into:
//          ["foo", "bar"] and "/locks/0123..."
func splitURLPath(urlPath string, maxDepth int) (folderPath []string, remainder string) {
	if !strings.HasPrefix(urlPath, "/") {
		// Really should start with "/"
		return nil, urlPath
	}
	p := strings.SplitN(urlPath, "/", maxDepth+2)
	// Skip the empty first one and the remainder in the last one
	for _, name := range p[1 : len(p)-1] {
		if isValidType(name) {
			// We found a part that is a special repo file or dir
			break
		}
		folderPath = append(folderPath, name)
	}
	// If the folder path is empty, the whole path is the remainder (do not strip '/')
	if len(folderPath) == 0 {
		return nil, urlPath
	}
	// Check that the urlPath starts with the reconstructed path, which should
	// always be the case.
	fullFolderPath := "/" + strings.Join(folderPath, "/")
	if !strings.HasPrefix(urlPath, fullFolderPath) {
		return nil, urlPath
	}
	return folderPath, urlPath[len(fullFolderPath):]
}

// folderPathValid checks if a folderPath returned by splitURLPath is valid and
// safe.
func folderPathValid(folderPath []string) bool {
	for _, name := range folderPath {
		if name == "" || name == ".." || name == "." || !valid(name) {
			return false
		}
	}
	return true
}
