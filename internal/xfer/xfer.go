// Package xfer copies files between any two filesystems.
//
// It is written against vfs.FS, so the four directions — local→local,
// local→remote, remote→local and remote→remote — are the same code (ADR 0001).
// The engine is deliberately sequential and honest about it: one item at a time,
// cancellable, with a conflict policy that is decided before anything is written.
// Parallel workers, resumable partial files and a queue come with M3; what is
// here is the part a copy cannot be correct without.
package xfer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/fakihariefnoto/omssh/internal/vfs"
)

// Action is what will happen to one item.
type Action int

// Actions.
const (
	// ActionCreate writes a destination that does not exist.
	ActionCreate Action = iota
	// ActionUpdate replaces an existing destination.
	ActionUpdate
	// ActionSkip leaves an existing destination alone.
	ActionSkip
)

// Label is a word for the action, for the plan view.
func (a Action) Label() string {
	switch a {
	case ActionCreate:
		return "create"
	case ActionUpdate:
		return "update"
	default:
		return "skip"
	}
}

// ConflictPolicy decides what a copy does when the destination exists.
type ConflictPolicy int

// Policies.
const (
	// PolicyAsk calls OnConflict for each conflicting item.
	PolicyAsk ConflictPolicy = iota
	// PolicyOverwrite always replaces.
	PolicyOverwrite
	// PolicySkip always keeps the destination.
	PolicySkip
	// PolicyUpdateIfNewer replaces only when the source is newer, by size or
	// modification time.
	PolicyUpdateIfNewer
)

// Item is one entry in a copy plan.
type Item struct {
	// Name is the path relative to the source directory.
	Name string
	Dir  bool
	Size int64
	// Exists is true when the destination already has this path.
	Exists bool
	// DstNewer is true when the destination is newer than the source.
	DstNewer bool
	// Action is what the plan will do.
	Action Action
}

// Plan is what a copy would do, before it does it.
type Plan struct {
	Items []Item
	Bytes int64
	// Directories that must be created.
	Dirs int
}

// Counts summarises the plan by action.
func (p Plan) Counts() map[Action]int {
	out := map[Action]int{}
	for _, item := range p.Items {
		out[item.Action]++
	}
	return out
}

// Request is one copy operation.
type Request struct {
	Src    vfs.FS
	SrcDir string
	Dst    vfs.FS
	DstDir string
	// Names are relative to SrcDir.
	Names []string
	// Policy decides conflicts.
	Policy ConflictPolicy
	// OnConflict is called for each conflicting item when Policy is PolicyAsk. A
	// nil callback with PolicyAsk behaves like PolicySkip.
	OnConflict func(Item) (ConflictPolicy, error)
	// Progress is called after each item, with the bytes done so far.
	Progress func(done, total int64, name string)
}

// Result is what a copy did.
type Result struct {
	Created int
	Updated int
	Skipped int
	Bytes   int64
	// Errors are per item: one unreadable file does not abort the rest.
	Errors []ItemError
}

// ItemError is a failure for one item, with the item's name.
type ItemError struct {
	Name string
	Err  error
}

// Error renders the failures, keeping each one wrapped so errors.Is still works
// on what the filesystem reported.
func (r Result) Error() error {
	if len(r.Errors) == 0 {
		return nil
	}
	errs := make([]error, 0, len(r.Errors))
	for _, item := range r.Errors {
		errs = append(errs, fmt.Errorf("%s: %w", item.Name, item.Err))
	}
	return fmt.Errorf("%d of the copy failed: %w", len(r.Errors), errors.Join(errs...))
}

// PlanCopy walks the sources and decides what would happen, without writing
// anything. It is what the interface shows before a copy starts.
func PlanCopy(ctx context.Context, req Request) (Plan, error) {
	plan := Plan{}
	for _, name := range req.Names {
		if err := ctx.Err(); err != nil {
			return plan, err
		}
		srcPath := req.Src.Join(req.SrcDir, name)
		dstPath := req.Dst.Join(req.DstDir, name)
		info, err := req.Src.Lstat(srcPath)
		if err != nil {
			return plan, fmt.Errorf("reading %s: %w", name, err)
		}
		if !info.IsDir() {
			action, dstInfo, err := decide(req, srcPath, dstPath, info)
			if err != nil {
				return plan, err
			}
			plan.Items = append(plan.Items, Item{
				Name: name, Size: info.Size(), Exists: dstInfo != nil,
				DstNewer: dstInfo != nil && dstInfo.ModTime().After(info.ModTime()),
				Action:   action,
			})
			if action != ActionSkip {
				plan.Bytes += info.Size()
			}
			continue
		}

		// A directory: the plan lists its files, and counts the directories that
		// must be created.
		plan.Dirs++
		err = fs.WalkDir(req.Src, srcPath, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if path == srcPath {
				return nil
			}
			rel := strings.TrimPrefix(path, srcPath+"/")
			item := Item{Name: name + "/" + rel, Dir: entry.IsDir()}
			if entry.IsDir() {
				plan.Dirs++
			} else if info, err := entry.Info(); err == nil {
				item.Size = info.Size()
			}
			dst := req.Dst.Join(req.DstDir, item.Name)
			if entry.IsDir() {
				if _, err := req.Dst.Lstat(dst); err == nil {
					item.Exists = true
				}
				plan.Items = append(plan.Items, item)
				return nil
			}
			srcInfo, err := entry.Info()
			if err != nil {
				return err
			}
			action, dstInfo, err := decide(req, path, dst, srcInfo)
			if err != nil {
				return err
			}
			item.Action = action
			item.Exists = dstInfo != nil
			item.DstNewer = dstInfo != nil && dstInfo.ModTime().After(srcInfo.ModTime())
			if action != ActionSkip {
				plan.Bytes += item.Size
			}
			plan.Items = append(plan.Items, item)
			return nil
		})
		if err != nil {
			return plan, fmt.Errorf("planning %s: %w", name, err)
		}
	}
	sort.SliceStable(plan.Items, func(i, j int) bool { return plan.Items[i].Name < plan.Items[j].Name })
	return plan, nil
}

// decide resolves one file's action against the destination.
func decide(req Request, srcPath, dstPath string, srcInfo fs.FileInfo) (Action, fs.FileInfo, error) {
	dstInfo, err := req.Dst.Lstat(dstPath)
	if err != nil {
		// A missing destination is not a conflict, and is not an error: the
		// filesystem was asked whether something is there and said no. Anything
		// else — a permission problem, a broken link — is worth reporting.
		if errors.Is(err, fs.ErrNotExist) {
			return ActionCreate, nil, nil
		}
		return ActionSkip, nil, fmt.Errorf("checking %s: %w", dstPath, err)
	}
	if dstInfo.IsDir() {
		// Replacing a directory with a file would be a destructive surprise; the
		// item is reported and skipped.
		return ActionSkip, dstInfo, nil
	}
	item := Item{
		Name:     srcPath,
		Size:     srcInfo.Size(),
		Exists:   true,
		DstNewer: dstInfo.ModTime().After(srcInfo.ModTime()),
	}

	policy := req.Policy
	if policy == PolicyAsk {
		if req.OnConflict == nil {
			return ActionSkip, dstInfo, nil
		}
		answered, err := req.OnConflict(item)
		if err != nil {
			return ActionSkip, dstInfo, err
		}
		policy = answered
	}

	switch policy {
	case PolicyOverwrite:
		return ActionUpdate, dstInfo, nil
	case PolicySkip:
		return ActionSkip, dstInfo, nil
	case PolicyUpdateIfNewer:
		if dstInfo.Size() == srcInfo.Size() && !srcInfo.ModTime().After(dstInfo.ModTime()) {
			return ActionSkip, dstInfo, nil
		}
		return ActionUpdate, dstInfo, nil
	default:
		return ActionSkip, dstInfo, nil
	}
}

// Copy runs the plan. It returns what it did, and any per-item failures.
func Copy(ctx context.Context, req Request, plan Plan) (Result, error) {
	result := Result{}
	var done int64
	buf := make([]byte, 1<<20)

	for i := range plan.Items {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		item := &plan.Items[i]
		if item.Action == ActionSkip {
			result.Skipped++
			continue
		}
		srcPath := req.Src.Join(req.SrcDir, item.Name)
		dstPath := req.Dst.Join(req.DstDir, item.Name)

		if item.Dir {
			if err := req.Dst.MkdirAll(dstPath, 0o755); err != nil {
				result.Errors = append(result.Errors, ItemError{Name: item.Name, Err: err})
			}
			continue
		}

		if err := req.Dst.MkdirAll(req.Dst.Dir(dstPath), 0o755); err != nil {
			result.Errors = append(result.Errors, ItemError{Name: item.Name, Err: err})
			continue
		}

		written, err := copyFile(ctx, req, srcPath, dstPath, buf, done, plan.Bytes, item.Name)
		if err != nil {
			// A cancelled copy is not an item failing: report the context, which
			// is what the caller asked about.
			if ctxErr := ctx.Err(); ctxErr != nil {
				return result, ctxErr
			}
			result.Errors = append(result.Errors, ItemError{Name: item.Name, Err: err})
			continue
		}
		done += written
		result.Bytes += written
		if item.Action == ActionCreate {
			result.Created++
		} else {
			result.Updated++
		}
		if req.Progress != nil {
			req.Progress(done, plan.Bytes, item.Name)
		}
	}
	if err := result.Error(); err != nil {
		return result, err
	}
	return result, nil
}

// copyFile streams one file, keeping its mode and time.
func copyFile(ctx context.Context, req Request, srcPath, dstPath string, buf []byte, done, total int64, name string) (int64, error) {
	src, err := req.Src.Open(srcPath)
	if err != nil {
		return 0, err
	}
	defer func() { _ = src.Close() }()

	info, err := src.Stat()
	if err != nil {
		return 0, err
	}

	dst, err := req.Dst.Create(dstPath)
	if err != nil {
		return 0, err
	}

	reader := io.Reader(&ctxReader{ctx: ctx, reader: src})
	if req.Progress != nil {
		// A single large file still moves: report while it is writing, throttled,
		// so a sidebar can show progress instead of an unexplained pause.
		reader = &progressReader{
			reader: reader,
			report: func(written int64) { req.Progress(done+written, total, name) },
		}
	}
	written, copyErr := io.CopyBuffer(dst, reader, buf)
	// The metadata belongs to a file that is fully written: setting it first
	// would be wiped by the write on some servers.
	_ = dst.Sync()
	if closeErr := dst.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		// A partial destination is worse than none: the next attempt would see a
		// truncated file and, with "update if newer", skip it.
		_ = req.Dst.Remove(dstPath)
		return written, copyErr
	}
	if req.Dst.Capabilities().Chmod {
		_ = req.Dst.Chmod(dstPath, info.Mode().Perm())
	}
	if req.Dst.Capabilities().Chtimes {
		_ = req.Dst.Chtimes(dstPath, info.ModTime(), info.ModTime())
	}
	return written, nil
}

// progressReader counts bytes and reports them no more than once per interval.
type progressReader struct {
	reader  io.Reader
	written int64
	last    time.Time
	report  func(written int64)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.reader.Read(b)
	p.written += int64(n)
	if p.report != nil && time.Since(p.last) >= DefaultProgressInterval {
		p.last = time.Now()
		p.report(p.written)
	}
	return n, err
}

// ctxReader stops a long copy when the context is cancelled, which is what makes
// a copy interruptible without closing the connection underneath it.
type ctxReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *ctxReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

// ErrNothingToCopy is returned when a request names no sources.
var ErrNothingToCopy = errors.New("xfer: nothing to copy")

// Validate checks a request before any work starts.
func (r Request) Validate() error {
	if r.Src == nil || r.Dst == nil {
		return errors.New("xfer: both filesystems are required")
	}
	if len(r.Names) == 0 {
		return ErrNothingToCopy
	}
	return nil
}

// DefaultProgressInterval is how often a long copy reports. Kept here so the
// caller throttles with the same number the engine expects.
const DefaultProgressInterval = 100 * time.Millisecond
