package webdav

import (
	"testing"

	"github.com/fakihariefnoto/omssh/internal/vfs/vfstest"
)

// The conformance suite every filesystem runs. WebDAV is allowed to refuse what it
// declares it cannot do, but nothing else.
func TestConformance(t *testing.T) {
	_, srv := newTestServer(t)
	fsys, err := New(Options{BaseURL: srv.URL + "/dav/", Username: "alice", Password: "secret", Label: "dav"})
	if err != nil {
		t.Fatal(err)
	}
	vfstest.Run(t, fsys)
}
