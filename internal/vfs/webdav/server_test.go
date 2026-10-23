package webdav

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// testServer is a small WebDAV server: PROPFIND with Depth 0 and 1, GET, PUT,
// DELETE, MKCOL, MOVE. It is a test double, not a server: enough to prove the
// client speaks the protocol.
type testServer struct {
	mu    sync.Mutex
	files map[string][]byte
	dirs  map[string]bool
	user  string
	pass  string
	base  string
}

func newTestServer(t *testing.T) (*testServer, *httptest.Server) {
	t.Helper()
	ts := &testServer{files: map[string][]byte{}, dirs: map[string]bool{"/": true}, user: "alice", pass: "secret"}
	srv := httptest.NewServer(http.HandlerFunc(ts.handle))
	t.Cleanup(srv.Close)
	ts.base = "/dav/"
	return ts, srv
}

func (s *testServer) handle(w http.ResponseWriter, r *http.Request) {
	if user, pass, ok := r.BasicAuth(); !ok || user != s.user || pass != s.pass {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	rel := strings.TrimPrefix(r.URL.Path, s.base)
	if !strings.HasPrefix(rel, "/") {
		rel = "/" + rel
	}
	rel = strings.TrimSuffix(rel, "/")
	if rel == "" {
		rel = "/"
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	switch r.Method {
	case "PROPFIND":
		depth := r.Header.Get("Depth")
		if depth == "" {
			depth = "1"
		}
		if !s.exists(rel) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		paths := []string{rel}
		if depth == "1" && s.dirs[rel] {
			for key := range s.files {
				if strings.HasPrefix(key, rel+"/") && !strings.Contains(strings.TrimPrefix(key, rel+"/"), "/") {
					paths = append(paths, key)
				}
			}
			for key := range s.dirs {
				if key != rel && strings.HasPrefix(key, rel+"/") && !strings.Contains(strings.TrimPrefix(key, rel+"/"), "/") {
					paths = append(paths, key)
				}
			}
		}
		sort.Strings(paths)
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.WriteHeader(http.StatusMultiStatus)
		fmt.Fprint(w, `<?xml version="1.0" encoding="utf-8"?><d:multistatus xmlns:d="DAV:">`)
		for _, p := range paths {
			dir := s.dirs[p]
			fmt.Fprintf(w, `<d:response><d:href>%s%s</d:href><d:propstat><d:prop><d:resourcetype>`, s.base, strings.TrimPrefix(p, "/"))
			if dir {
				fmt.Fprint(w, `<d:collection/>`)
			}
			fmt.Fprint(w, `</d:resourcetype>`)
			if !dir {
				fmt.Fprintf(w, `<d:getcontentlength>%d</d:getcontentlength>`, len(s.files[p]))
			}
			fmt.Fprintf(w, `<d:getlastmodified>%s</d:getlastmodified>`, time.Now().UTC().Format(http.TimeFormat))
			fmt.Fprint(w, `</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`)
		}
		fmt.Fprint(w, `</d:multistatus>`)
	case http.MethodGet:
		data, ok := s.files[rel]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(data)))
		w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
		_, _ = w.Write(data)
	case http.MethodPut:
		if !s.dirs[parentOf(rel)] {
			w.WriteHeader(http.StatusConflict)
			return
		}
		body, _ := io.ReadAll(r.Body)
		s.files[rel] = body
		w.WriteHeader(http.StatusCreated)
	case http.MethodDelete:
		if !s.exists(rel) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		for key := range s.files {
			if key == rel || strings.HasPrefix(key, rel+"/") {
				delete(s.files, key)
			}
		}
		for key := range s.dirs {
			if key == rel || strings.HasPrefix(key, rel+"/") {
				delete(s.dirs, key)
			}
		}
		w.WriteHeader(http.StatusNoContent)
	case "MKCOL":
		if s.exists(rel) {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !s.dirs[parentOf(rel)] {
			w.WriteHeader(http.StatusConflict)
			return
		}
		s.dirs[rel] = true
		w.WriteHeader(http.StatusCreated)
	case "MOVE":
		dest := r.Header.Get("Destination")
		if parsed, err := url.Parse(dest); err == nil {
			dest = parsed.Path
		}
		dest = strings.TrimPrefix(dest, s.base)
		dest = strings.TrimSuffix(dest, "/")
		if !strings.HasPrefix(dest, "/") {
			dest = "/" + dest
		}
		data, ok := s.files[rel]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		s.files[dest] = data
		delete(s.files, rel)
		w.WriteHeader(http.StatusCreated)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *testServer) exists(p string) bool {
	return s.files[p] != nil || s.dirs[p]
}

func parentOf(p string) string {
	i := strings.LastIndex(p, "/")
	if i <= 0 {
		return "/"
	}
	return p[:i]
}
