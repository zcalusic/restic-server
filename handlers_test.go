package restserver

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestJoin(t *testing.T) {
	var tests = []struct {
		base   string
		names  []string
		result string
	}{
		{"/", []string{"foo", "bar"}, "/foo/bar"},
		{"/srv/server", []string{"foo", "bar"}, "/srv/server/foo/bar"},
		{"/srv/server", []string{"foo", "..", "bar"}, "/srv/server/foo/bar"},
		{"/srv/server", []string{"..", "bar"}, "/srv/server/bar"},
		{"/srv/server", []string{".."}, "/srv/server"},
		{"/srv/server", []string{"..", ".."}, "/srv/server"},
		{"/srv/server", []string{"repo", "data"}, "/srv/server/repo/data"},
		{"/srv/server", []string{"repo", "data", "..", ".."}, "/srv/server/repo/data"},
		{"/srv/server", []string{"repo", "data", "..", "data", "..", "..", ".."}, "/srv/server/repo/data/data"},
	}

	for _, test := range tests {
		t.Run("", func(t *testing.T) {
			got, err := join(filepath.FromSlash(test.base), test.names...)
			if err != nil {
				t.Fatal(err)
			}

			want := filepath.FromSlash(test.result)
			if got != want {
				t.Fatalf("wrong result returned, want %v, got %v", want, got)
			}
		})
	}
}

// declare a few helper functions

// wantFunc tests the HTTP response in res and calls t.Error() if something is incorrect.
type wantFunc func(t testing.TB, res *httptest.ResponseRecorder)

// newRequest returns a new HTTP request with the given params. On error, t.Fatal is called.
func newRequest(t testing.TB, method, path string, body io.Reader) *http.Request {
	req, err := http.NewRequest(method, path, body)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

// wantCode returns a function which checks that the response has the correct HTTP status code.
func wantCode(code int) wantFunc {
	return func(t testing.TB, res *httptest.ResponseRecorder) {
		if res.Code != code {
			t.Errorf("wrong response code, want %v, got %v", code, res.Code)
		}
	}
}

// wantBody returns a function which checks that the response has the data in the body.
func wantBody(body string) wantFunc {
	return func(t testing.TB, res *httptest.ResponseRecorder) {
		if res.Body == nil {
			t.Errorf("body is nil, want %q", body)
			return
		}

		if !bytes.Equal(res.Body.Bytes(), []byte(body)) {
			t.Errorf("wrong response body, want:\n  %q\ngot:\n  %q", body, res.Body.Bytes())
		}
	}
}

// checkRequest uses f to process the request and runs the checker functions on the result.
func checkRequest(t testing.TB, f http.HandlerFunc, req *http.Request, want []wantFunc) {
	rr := httptest.NewRecorder()
	f(rr, req)

	for _, fn := range want {
		fn(t, rr)
	}
}

// TestRequest is a sequence of HTTP requests with (optional) tests for the response.
type TestRequest struct {
	req  *http.Request
	want []wantFunc
}

// createOverwriteDeleteSeq returns a sequence which will create a new file at
// path, and then try to overwrite and delete it.
func createOverwriteDeleteSeq(t testing.TB, path string) []TestRequest {
	// add a file, try to overwrite and delete it
	req := []TestRequest{
		{
			req:  newRequest(t, "GET", path, nil),
			want: []wantFunc{wantCode(http.StatusNotFound)},
		},
		{
			req:  newRequest(t, "POST", path, strings.NewReader("foobar test config")),
			want: []wantFunc{wantCode(http.StatusOK)},
		},
		{
			req: newRequest(t, "GET", path, nil),
			want: []wantFunc{
				wantCode(http.StatusOK),
				wantBody("foobar test config"),
			},
		},
		{
			req:  newRequest(t, "POST", path, strings.NewReader("other config")),
			want: []wantFunc{wantCode(http.StatusForbidden)},
		},
		{
			req: newRequest(t, "GET", path, nil),
			want: []wantFunc{
				wantCode(http.StatusOK),
				wantBody("foobar test config"),
			},
		},
		{
			req:  newRequest(t, "DELETE", path, nil),
			want: []wantFunc{wantCode(http.StatusForbidden)},
		},
		{
			req: newRequest(t, "GET", path, nil),
			want: []wantFunc{
				wantCode(http.StatusOK),
				wantBody("foobar test config"),
			},
		},
	}
	return req
}

// TestResticHandler runs tests on the restic handler code, especially in append-only mode.
func TestResticHandler(t *testing.T) {
	buf := make([]byte, 32)
	_, err := io.ReadFull(rand.Reader, buf)
	if err != nil {
		t.Fatal(err)
	}
	randomID := hex.EncodeToString(buf)

	var tests = []struct {
		seq []TestRequest
	}{
		{createOverwriteDeleteSeq(t, "/config")},
		{createOverwriteDeleteSeq(t, "/data/"+randomID)},
		{
			// ensure we can add and remove lock files
			[]TestRequest{
				{
					req:  newRequest(t, "GET", "/locks/"+randomID, nil),
					want: []wantFunc{wantCode(http.StatusNotFound)},
				},
				{
					req:  newRequest(t, "POST", "/locks/"+randomID, strings.NewReader("lock file")),
					want: []wantFunc{wantCode(http.StatusOK)},
				},
				{
					req: newRequest(t, "GET", "/locks/"+randomID, nil),
					want: []wantFunc{
						wantCode(http.StatusOK),
						wantBody("lock file"),
					},
				},
				{
					req:  newRequest(t, "POST", "/locks/"+randomID, strings.NewReader("other lock file")),
					want: []wantFunc{wantCode(http.StatusForbidden)},
				},
				{
					req:  newRequest(t, "DELETE", "/locks/"+randomID, nil),
					want: []wantFunc{wantCode(http.StatusOK)},
				},
				{
					req:  newRequest(t, "GET", "/locks/"+randomID, nil),
					want: []wantFunc{wantCode(http.StatusNotFound)},
				},
			},
		},
	}

	// setup rclone with a local backend in a temporary directory
	tempdir, err := ioutil.TempDir("", "rclone-restic-test-")
	if err != nil {
		t.Fatal(err)
	}

	// make sure the tempdir is properly removed
	defer func() {
		err := os.RemoveAll(tempdir)
		if err != nil {
			t.Fatal(err)
		}
	}()

	// set append-only mode and configure path
	mux, err := NewHandler(&Server{
		AppendOnly:   true,
		Path:         tempdir,
		NoAuth:       true,
		Debug:        true,
		PanicOnError: true,
	})
	if err != nil {
		t.Fatalf("error from NewHandler: %v", err)
	}

	// create the repo
	checkRequest(t, mux.ServeHTTP,
		newRequest(t, "POST", "/?create=true", nil),
		[]wantFunc{wantCode(http.StatusOK)})

	for _, test := range tests {
		t.Run("", func(t *testing.T) {
			for i, seq := range test.seq {
				t.Logf("request %v: %v %v", i, seq.req.Method, seq.req.URL.Path)
				checkRequest(t, mux.ServeHTTP, seq.req, seq.want)
			}
		})
	}
}

func TestSplitURLPath(t *testing.T) {
	var tests = []struct {
		// Params
		urlPath  string
		maxDepth int
		// Expected result
		folderPath []string
		remainder  string
	}{
		{"/", 0, nil, "/"},
		{"/", 2, nil, "/"},
		{"/foo/bar/locks/0123", 0, nil, "/foo/bar/locks/0123"},
		{"/foo/bar/locks/0123", 1, []string{"foo"}, "/bar/locks/0123"},
		{"/foo/bar/locks/0123", 2, []string{"foo", "bar"}, "/locks/0123"},
		{"/foo/bar/locks/0123", 3, []string{"foo", "bar"}, "/locks/0123"},
		{"/foo/bar/zzz/locks/0123", 2, []string{"foo", "bar"}, "/zzz/locks/0123"},
		{"/foo/bar/zzz/locks/0123", 3, []string{"foo", "bar", "zzz"}, "/locks/0123"},
		{"/foo/bar/locks/", 2, []string{"foo", "bar"}, "/locks/"},
		{"/foo/locks/", 2, []string{"foo"}, "/locks/"},
		{"/foo/data/", 2, []string{"foo"}, "/data/"},
		{"/foo/index/", 2, []string{"foo"}, "/index/"},
		{"/foo/keys/", 2, []string{"foo"}, "/keys/"},
		{"/foo/snapshots/", 2, []string{"foo"}, "/snapshots/"},
		{"/foo/config", 2, []string{"foo"}, "/config"},
		{"/foo/", 2, []string{"foo"}, "/"},
		{"/foo/bar/", 2, []string{"foo", "bar"}, "/"},
		{"/foo/bar", 2, []string{"foo"}, "/bar"},
		{"/locks/", 2, nil, "/locks/"},
		// This function only splits, it does not check the path components!
		{"/././locks/", 2, []string{"..", ".."}, "/locks/"},
		{"/../../locks/", 2, []string{"..", ".."}, "/locks/"},
		{"///locks/", 2, []string{"", ""}, "/locks/"},
		{"////locks/", 2, []string{"", ""}, "//locks/"},
		// Robustness against broken input
		{"/", -42, nil, "/"},
		{"foo", 2, nil, "foo"},
		{"foo/bar", 2, nil, "foo/bar"},
		{"", 2, nil, ""},
	}

	for i, test := range tests {
		t.Run(fmt.Sprintf("test-%d", i), func(t *testing.T) {
			folderPath, remainder := splitURLPath(test.urlPath, test.maxDepth)

			var fpEqual bool
			if len(test.folderPath) == 0 && len(folderPath) == 0 {
				fpEqual = true // this check allows for nil vs empty slice
			} else {
				fpEqual = reflect.DeepEqual(test.folderPath, folderPath)
			}
			if !fpEqual {
				t.Errorf("wrong folderPath: want %v, got %v", test.folderPath, folderPath)
			}

			if test.remainder != remainder {
				t.Errorf("wrong remainder: want %v, got %v", test.remainder, remainder)
			}
		})
	}
}
