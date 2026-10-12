package resource

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Finding 3.1: a server that stops sending fails the download, whether it never answers or stops
// in the middle of the body; a slow server that keeps sending does not, however long it takes.
func TestDownloadTimeouts(t *testing.T) {
	stop := make(chan struct{})
	defer close(stop)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wait := func() {
			select {
			case <-stop:
			case <-r.Context().Done():
			}
		}
		switch r.URL.Path {
		case "/silent.tar.gz":
			wait()
		case "/stops.tar.gz":
			w.Header().Set("Content-Length", "1000")
			w.Write([]byte("partial"))
			w.(http.Flusher).Flush()
			wait()
		case "/slow.tar.gz":
			for range 8 {
				w.Write([]byte("x"))
				w.(http.Flusher).Flush()
				time.Sleep(50 * time.Millisecond)
			}
		}
	}))
	defer server.Close()

	fm := NewFetchManager(t.TempDir())
	fm.idleTimeout = 200 * time.Millisecond
	for _, name := range []string{"silent", "stops"} {
		start := time.Now()
		err := fm.downloadFile(context.Background(), server.URL+"/"+name+".tar.gz", filepath.Join(t.TempDir(), "f"))
		if err == nil || !strings.Contains(err.Error(), "sent nothing for 200ms") {
			t.Errorf("%s: err = %v", name, err)
		}
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Errorf("%s: gave up after %s", name, elapsed)
		}
	}
	if err := fm.downloadFile(context.Background(), server.URL+"/slow.tar.gz", filepath.Join(t.TempDir(), "f")); err != nil {
		t.Errorf("a slow download that took longer than the idle timeout failed: %v", err)
	}

	// An interrupt stops a download, and is reported as one
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	fm.idleTimeout = time.Minute
	err := fm.downloadFile(ctx, server.URL+"/silent.tar.gz", filepath.Join(t.TempDir(), "f"))
	if !errors.Is(err, context.Canceled) {
		t.Errorf("interrupted: err = %v", err)
	}
}
