// Package deploy copies a local tree to a remote one, and says what it would do
// before it does it. It is a copy with a policy: which files, what happens to the
// ones that are already there, and — in sync mode — what to remove.
//
// It deliberately shares the copy engine's shape (describe, then act), so a deploy
// can be reviewed with the same habits a copy is.
package deploy

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"path"
	"strings"
	"time"

	"github.com/fakihariefnoto/omssh/internal/vfs"
)

// Kind is what a deploy would do to one path.
type Kind int

// The kinds, in the order a person reads them.
const (
	Same Kind = iota
	Create
	Update
	Delete
)

// String names the kind the way the plan shows it.
func (k Kind) String() string {
	switch k {
	case Create:
		return "create"
	case Update:
		return "update"
	case Delete:
		return "delete"
	default:
		return "same"
	}
}

// Change is one path in the plan.
type Change struct {
	Path string // relative to the roots, slash-separated
	Kind Kind
	Size int64
}

// Plan is what a deploy would do.
type Plan struct {
	Changes []Change
	Bytes   int64 // bytes that will cross the link
}

// Counts summarises the plan.
func (p Plan) Counts() (created, updated, removed int) {
	for _, change := range p.Changes {
		switch change.Kind {
		case Create:
			created++
		case Update:
			updated++
		case Delete:
			removed++
		case Same:
		}
	}
	return created, updated, removed
}

// Options decides which files take part.
type Options struct {
	LocalRoot     string
	RemoteRoot    string
	Exclude       []string
	IncludeHidden bool
	// Delete removes remote files that are not in the local tree. That is what
	// makes a deploy a mirror rather than an overlay, and it is off by default
	// because it is the one part that loses data.
	Delete bool
}

// Diff walks the local tree and compares it with the remote one. It never writes.
func Diff(ctx context.Context, local, remote vfs.FS, opts Options) (Plan, error) {
	plan := Plan{}
	seen := map[string]bool{}

	err := walkFiles(ctx, local, opts.LocalRoot, "", opts, func(rel string, info fs.FileInfo) error {
		seen[rel] = true
		target := remote.Join(opts.RemoteRoot, rel)
		remoteInfo, err := remote.Stat(target)
		switch {
		case err != nil:
			plan.Changes = append(plan.Changes, Change{Path: rel, Kind: Create, Size: info.Size()})
			plan.Bytes += info.Size()
		case remoteInfo.IsDir():
			return nil
		case differs(info, remoteInfo):
			plan.Changes = append(plan.Changes, Change{Path: rel, Kind: Update, Size: info.Size()})
			plan.Bytes += info.Size()
		default:
			plan.Changes = append(plan.Changes, Change{Path: rel, Kind: Same, Size: info.Size()})
		}
		return nil
	})
	if err != nil {
		return plan, err
	}

	if !opts.Delete {
		return plan, nil
	}
	// Sync mode: anything on the remote side that the local tree does not have
	// goes. Excluded names are left alone — an exclusion that only worked in one
	// direction would delete exactly what it was meant to protect.
	err = walkRemote(ctx, remote, opts.RemoteRoot, "", opts, func(rel string, size int64) error {
		if seen[rel] {
			return nil
		}
		plan.Changes = append(plan.Changes, Change{Path: rel, Kind: Delete, Size: size})
		return nil
	})
	return plan, err
}

// differs decides whether a remote file needs the local one. Size is the honest
// test; modification time is the tie-breaker, with a second of slack because
// filesystems disagree about sub-second precision.
func differs(local, remote fs.FileInfo) bool {
	if local.Size() != remote.Size() {
		return true
	}
	if local.ModTime().IsZero() || remote.ModTime().IsZero() {
		return false
	}
	return local.ModTime().Truncate(time.Second).After(remote.ModTime().Truncate(time.Second))
}

// Apply performs the plan. It re-walks rather than trusting a stale plan: the
// files may have changed between the review and the Enter key.
func Apply(ctx context.Context, local, remote vfs.FS, opts Options, progress func(change Change)) (Plan, error) {
	plan, err := Diff(ctx, local, remote, opts)
	if err != nil {
		return plan, err
	}
	var failures []error
	for _, change := range plan.Changes {
		if err := ctx.Err(); err != nil {
			return plan, err
		}
		if change.Kind == Same {
			continue
		}
		if progress != nil {
			progress(change)
		}
		src := local.Join(opts.LocalRoot, change.Path)
		dst := remote.Join(opts.RemoteRoot, change.Path)
		var err error
		switch change.Kind {
		case Create, Update:
			err = copyFile(local, src, remote, dst)
		case Delete:
			err = remote.RemoveAll(dst)
		case Same:
		}
		if err != nil {
			failures = append(failures, errors.New(change.Path+": "+err.Error()))
		}
	}
	return plan, errors.Join(failures...)
}

// copyFile copies one file, creating the directory above it.
func copyFile(srcFS vfs.FS, srcPath string, dstFS vfs.FS, dstPath string) error {
	if err := dstFS.MkdirAll(dstFS.Dir(dstPath), 0o755); err != nil {
		return err
	}
	info, err := srcFS.Stat(srcPath)
	if err != nil {
		return err
	}
	src, err := srcFS.Open(srcPath)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()

	mode := info.Mode().Perm()
	if mode == 0 {
		mode = 0o644
	}
	dst, err := dstFS.OpenFile(dstPath, vfs.O_WRONLY|vfs.O_CREATE|vfs.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		return err
	}
	return dst.Close()
}

// walkFiles visits the regular files under root, naming them relative to it.
func walkFiles(ctx context.Context, fsys vfs.FS, root, rel string, opts Options, visit func(rel string, info fs.FileInfo) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dir := fsys.Join(root, rel)
	entries, err := fsys.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		childRel := path.Join(rel, name)
		if opts.excluded(childRel, name) {
			continue
		}
		if !opts.IncludeHidden && strings.HasPrefix(name, ".") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.IsDir() {
			if err := walkFiles(ctx, fsys, root, childRel, opts, visit); err != nil {
				return err
			}
			continue
		}
		if err := visit(childRel, info); err != nil {
			return err
		}
	}
	return nil
}

// walkRemote visits remote files the same way, so a delete list can be built.
func walkRemote(ctx context.Context, fsys vfs.FS, root, rel string, opts Options, visit func(rel string, size int64) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dir := fsys.Join(root, rel)
	entries, err := fsys.ReadDir(dir)
	if err != nil && rel == "" {
		// Nothing there yet: a first deploy deletes nothing.
		return nil // a missing remote root is an empty remote tree
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		childRel := path.Join(rel, name)
		if opts.excluded(childRel, name) {
			continue
		}
		if !opts.IncludeHidden && strings.HasPrefix(name, ".") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.IsDir() {
			if err := walkRemote(ctx, fsys, root, childRel, opts, visit); err != nil {
				return err
			}
			continue
		}
		if err := visit(childRel, info.Size()); err != nil {
			return err
		}
	}
	return nil
}

// excluded matches a pattern against the base name and against the relative
// path, so "node_modules" works at any depth and "build/*.map" works where it was
// written.
func (o Options) excluded(rel, name string) bool {
	for _, pattern := range o.Exclude {
		if matched, _ := path.Match(pattern, name); matched {
			return true
		}
		if matched, _ := path.Match(pattern, rel); matched {
			return true
		}
		if strings.HasSuffix(pattern, "/") {
			if matched, _ := path.Match(strings.TrimSuffix(pattern, "/"), path.Dir(rel)); matched {
				return true
			}
		}
	}
	return false
}
