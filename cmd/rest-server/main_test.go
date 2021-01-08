package main

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	restserver "github.com/restic/rest-server"
)

func TestGetHandler(t *testing.T) {
	dir, err := ioutil.TempDir("", "rest-server-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(dir)

	getHandler := restserver.NewHandler

	// With NoAuth = false and no .htpasswd
	_, err = getHandler(&restserver.Server{Path: dir})
	if err == nil {
		t.Errorf("NoAuth=false: expected error, got nil")
	}

	// With NoAuth = true and no .htpasswd
	_, err = getHandler(&restserver.Server{NoAuth: true, Path: dir})
	if err != nil {
		t.Errorf("NoAuth=true: expected no error, got %v", err)
	}

	// Create .htpasswd
	htpasswd := filepath.Join(dir, ".htpasswd")
	err = ioutil.WriteFile(htpasswd, []byte(""), 0644)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(htpasswd)

	// With NoAuth = false and with .htpasswd
	_, err = getHandler(&restserver.Server{Path: dir})
	if err != nil {
		t.Errorf("NoAuth=false with .htpasswd: expected no error, got %v", err)
	}
}
