package restserver

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func TestIsUserPath(t *testing.T) {
	var tests = []struct {
		username string
		path     string
		result   bool
	}{
		{"foo", "/", false},
		{"foo", "/foo", true},
		{"foo", "/foo/", true},
		{"foo", "/foo/bar", true},
		{"foo", "/foobar", false},
	}

	for _, test := range tests {
		result := isUserPath(test.username, test.path)
		if result != test.result {
			t.Errorf("isUserPath(%q, %q) was incorrect, got: %v, want: %v.", test.username, test.path, result, test.result)
		}
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
		t.Helper()
		if res.Code != code {
			t.Errorf("wrong response code, want %v, got %v", code, res.Code)
		}
	}
}

// wantBody returns a function which checks that the response has the data in the body.
func wantBody(body string) wantFunc {
	return func(t testing.TB, res *httptest.ResponseRecorder) {
		t.Helper()
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
	t.Helper()
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

	// setup the server with a local backend in a temporary directory
	tempdir, err := ioutil.TempDir("", "rest-server-test-")
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
	mux := NewHandler(Server{
		AppendOnly: true,
		Path:       tempdir,
	})

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

// delayErrorReader blocks until Continue is closed, closes the channel FirstRead and then returns Err.
type delayErrorReader struct {
	FirstRead     chan struct{}
	firstReadOnce sync.Once

	Err error

	Continue chan struct{}
}

func newDelayedErrorReader(err error) *delayErrorReader {
	return &delayErrorReader{
		Err:       err,
		Continue:  make(chan struct{}),
		FirstRead: make(chan struct{}),
	}
}

func (d *delayErrorReader) Read(p []byte) (int, error) {
	d.firstReadOnce.Do(func() {
		// close the channel to signal that the first read has happened
		close(d.FirstRead)
	})
	<-d.Continue
	return 0, d.Err
}

// TestAbortedRequest runs tests with concurrent upload requests for the same file.
func TestAbortedRequest(t *testing.T) {
	// setup the server with a local backend in a temporary directory
	tempdir, err := ioutil.TempDir("", "rest-server-test-")
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
	mux := NewHandler(Server{
		Path: tempdir,
	})

	// create the repo
	checkRequest(t, mux.ServeHTTP,
		newRequest(t, "POST", "/?create=true", nil),
		[]wantFunc{wantCode(http.StatusOK)})

	var (
		id = "b5bb9d8014a0f9b1d61e21e796d78dccdf1352f23cd32812f4850b878ae4944c"
		wg sync.WaitGroup
	)

	// the first request is an upload to a file which blocks while reading the
	// body and then after some data returns an error
	rd := newDelayedErrorReader(errors.New("injected"))

	wg.Add(1)
	go func() {
		defer wg.Done()

		// first, read some string, then read from rd (which blocks and then
		// returns an error)
		dataReader := io.MultiReader(strings.NewReader("invalid data from aborted request\n"), rd)

		t.Logf("start first upload")
		req := newRequest(t, "POST", "/data/"+id, dataReader)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		t.Logf("first upload done, response %v (%v)", rr.Code, rr.Result().Status)
	}()

	// wait until the first request starts reading from the body
	<-rd.FirstRead

	// then while the first request is blocked we send a second request to
	// delete the file and a third request to upload to the file again, only
	// then the first request is unblocked.

	t.Logf("delete file")
	checkRequest(t, mux.ServeHTTP,
		newRequest(t, "DELETE", "/data/"+id, nil),
		nil) // don't check anything, restic also ignores errors here

	t.Logf("upload again")
	checkRequest(t, mux.ServeHTTP,
		newRequest(t, "POST", "/data/"+id, strings.NewReader("foo\n")),
		[]wantFunc{wantCode(http.StatusOK)})

	// unblock the reader for the first request now so it can continue
	close(rd.Continue)

	// wait for the first request to continue
	wg.Wait()

	// request the file again, it must exist and contain the string from the
	// second request
	checkRequest(t, mux.ServeHTTP,
		newRequest(t, "GET", "/data/"+id, nil),
		[]wantFunc{
			wantCode(http.StatusOK),
			wantBody("foo\n"),
		},
	)
}
