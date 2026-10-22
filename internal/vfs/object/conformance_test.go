package object

import (
	"testing"

	"github.com/fakihariefnoto/omssh/internal/vfs/vfstest"
)

// The conformance suite every filesystem runs. Object storage is allowed to fail
// the parts it declares it cannot do, but nothing else.
func TestConformance(t *testing.T) {
	fsys, _ := testFS(t)
	vfstest.Run(t, fsys)
}
