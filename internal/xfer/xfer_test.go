package xfer_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fakihariefnoto/omssh/internal/vfs"
	"github.com/fakihariefnoto/omssh/internal/xfer"
)

// pair is two rooted local filesystems, standing in for any two sides.
func pair(t *testing.T) (src, dst vfs.FS, srcDir, dstDir string) {
	t.Helper()
	srcDir, dstDir = t.TempDir(), t.TempDir()
	src, err := vfs.NewLocal(srcDir)
	if err != nil {
		t.Fatal(err)
	}
	dst, err = vfs.NewLocal(dstDir)
	if err != nil {
		t.Fatal(err)
	}
	return src, dst, srcDir, dstDir
}

func write(t *testing.T, fsys vfs.FS, name, body string) {
	t.Helper()
	if err := fsys.MkdirAll(fsys.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := vfs.WriteFile(fsys, name, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, fsys vfs.FS, name string) string {
	t.Helper()
	body, err := vfs.ReadAll(fsys, name)
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(body)
}

func request(src, dst vfs.FS, names ...string) xfer.Request {
	return xfer.Request{Src: src, SrcDir: "/", Dst: dst, DstDir: "/", Names: names, Policy: xfer.PolicyOverwrite}
}

// Every direction is the same code, so the ordinary case is worth pinning
// precisely: bytes, mode and time all arrive.
func TestCopyOneFile(t *testing.T) {
	src, dst, _, _ := pair(t)
	write(t, src, "/notes.txt", "hello\n")
	when := time.Date(2026, 5, 4, 3, 2, 1, 0, time.UTC)
	if err := src.Chtimes("/notes.txt", when, when); err != nil {
		t.Fatal(err)
	}

	req := request(src, dst, "notes.txt")
	plan, err := xfer.PlanCopy(context.Background(), req)
	if err != nil {
		t.Fatalf("PlanCopy: %v", err)
	}
	if len(plan.Items) != 1 || plan.Items[0].Action != xfer.ActionCreate {
		t.Fatalf("plan = %+v", plan.Items)
	}
	if plan.Bytes != 6 {
		t.Errorf("plan bytes = %d, want 6", plan.Bytes)
	}

	result, err := xfer.Copy(context.Background(), req, plan)
	if err != nil {
		t.Fatalf("Copy: %v", err)
	}
	if result.Created != 1 || result.Updated != 0 {
		t.Errorf("result = %+v", result)
	}
	if got := read(t, dst, "/notes.txt"); got != "hello\n" {
		t.Errorf("destination holds %q", got)
	}
	info, err := dst.Stat("/notes.txt")
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v, want 0644", info.Mode().Perm())
	}
	if !info.ModTime().Equal(when) {
		t.Errorf("mtime = %v, want %v", info.ModTime(), when)
	}
}

// A directory arrives with its shape: files, nested directories, and the
// directory's own permissions.
func TestCopyTree(t *testing.T) {
	src, dst, _, _ := pair(t)
	write(t, src, "/site/index.html", "<html></html>")
	write(t, src, "/site/css/app.css", "body{}")
	write(t, src, "/site/a/b/deep.txt", "deep")
	if err := src.Mkdir("/site/empty", 0o750); err != nil {
		t.Fatal(err)
	}

	req := request(src, dst, "site")
	plan, err := xfer.PlanCopy(context.Background(), req)
	if err != nil {
		t.Fatalf("PlanCopy: %v", err)
	}
	if plan.Dirs < 4 {
		t.Errorf("the plan should count the directories, got %d", plan.Dirs)
	}
	result, err := xfer.Copy(context.Background(), req, plan)
	if err != nil {
		t.Fatalf("Copy: %v", err)
	}
	if result.Created != 3 {
		t.Errorf("created %d files, want 3", result.Created)
	}
	if got := read(t, dst, "/site/a/b/deep.txt"); got != "deep" {
		t.Errorf("nested file holds %q", got)
	}
	if info, err := dst.Stat("/site/empty"); err != nil || !info.IsDir() {
		t.Errorf("the empty directory did not arrive: %v", err)
	}
}

// An existing destination is a conflict, and the policy decides.
func TestConflictPolicies(t *testing.T) {
	src, dst, _, _ := pair(t)
	write(t, src, "/same.txt", "new body")
	write(t, dst, "/same.txt", "old body")

	t.Run("overwrite", func(t *testing.T) {
		req := request(src, dst, "same.txt")
		plan, err := xfer.PlanCopy(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if plan.Items[0].Action != xfer.ActionUpdate {
			t.Fatalf("expected update, got %v", plan.Items[0].Action)
		}
		if _, err := xfer.Copy(context.Background(), req, plan); err != nil {
			t.Fatal(err)
		}
		if got := read(t, dst, "/same.txt"); got != "new body" {
			t.Errorf("destination holds %q", got)
		}
	})

	t.Run("skip", func(t *testing.T) {
		write(t, dst, "/same.txt", "keep me")
		req := request(src, dst, "same.txt")
		req.Policy = xfer.PolicySkip
		plan, err := xfer.PlanCopy(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if plan.Items[0].Action != xfer.ActionSkip {
			t.Fatalf("expected skip, got %v", plan.Items[0].Action)
		}
		result, err := xfer.Copy(context.Background(), req, plan)
		if err != nil {
			t.Fatal(err)
		}
		if result.Skipped != 1 {
			t.Errorf("skipped %d, want 1", result.Skipped)
		}
		if got := read(t, dst, "/same.txt"); got != "keep me" {
			t.Errorf("a skipped file changed: %q", got)
		}
	})

	t.Run("update if newer", func(t *testing.T) {
		// The destination is newer and the same size: it must be left alone.
		write(t, src, "/same.txt", "old body")
		write(t, dst, "/same.txt", "new body")
		now := time.Now()
		if err := src.Chtimes("/same.txt", now.Add(-time.Hour), now.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
		if err := dst.Chtimes("/same.txt", now, now); err != nil {
			t.Fatal(err)
		}
		req := request(src, dst, "same.txt")
		req.Policy = xfer.PolicyUpdateIfNewer
		plan, err := xfer.PlanCopy(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if plan.Items[0].Action != xfer.ActionSkip {
			t.Fatalf("a newer destination should be skipped, got %v", plan.Items[0].Action)
		}
		if _, err := xfer.Copy(context.Background(), req, plan); err != nil {
			t.Fatal(err)
		}
		if got := read(t, dst, "/same.txt"); got != "new body" {
			t.Errorf("a newer destination was overwritten: %q", got)
		}
	})

	t.Run("ask", func(t *testing.T) {
		write(t, dst, "/same.txt", "old body")
		req := request(src, dst, "same.txt")
		req.Policy = xfer.PolicyAsk
		asked := 0
		req.OnConflict = func(item xfer.Item) (xfer.ConflictPolicy, error) {
			asked++
			if !item.Exists || item.Size != 8 {
				t.Errorf("the conflict should describe the item: %+v", item)
			}
			return xfer.PolicyOverwrite, nil
		}
		plan, err := xfer.PlanCopy(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if asked != 1 {
			t.Errorf("asked %d times, want 1", asked)
		}
		if plan.Items[0].Action != xfer.ActionUpdate {
			t.Errorf("the answer should have been applied, got %v", plan.Items[0].Action)
		}
	})

	t.Run("ask without a callback keeps the destination", func(t *testing.T) {
		write(t, dst, "/same.txt", "keep me")
		req := request(src, dst, "same.txt")
		req.Policy = xfer.PolicyAsk
		plan, err := xfer.PlanCopy(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if plan.Items[0].Action != xfer.ActionSkip {
			t.Errorf("no callback should mean skip, got %v", plan.Items[0].Action)
		}
	})
}

// slowFS makes reads take time, so a copy can be interrupted reliably. A local
// copy of any size finishes in microseconds otherwise.
type slowFS struct {
	vfs.FS
	delay time.Duration
}

func (s slowFS) Open(name string) (fs.File, error) {
	file, err := s.FS.Open(name)
	if err != nil {
		return nil, err
	}
	return &slowFile{File: file, delay: s.delay}, nil
}

type slowFile struct {
	fs.File
	delay time.Duration
}

func (f *slowFile) Read(p []byte) (int, error) {
	time.Sleep(f.delay)
	return f.File.Read(p)
}

// A cancellation stops the copy mid-stream, reports why, and leaves no partial
// destination behind.
func TestCancellation(t *testing.T) {
	src, dst, _, _ := pair(t)
	body := make([]byte, 16<<20)
	write(t, src, "/big.bin", string(body))

	req := request(slowFS{FS: src, delay: 5 * time.Millisecond}, dst, "big.bin")
	plan, err := xfer.PlanCopy(context.Background(), xfer.Request{
		Src: src, SrcDir: "/", Dst: dst, DstDir: "/", Names: []string{"big.bin"},
		Policy: xfer.PolicyOverwrite,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Millisecond)
	defer cancel()
	_, err = xfer.Copy(ctx, req, plan)
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("an interrupted copy should report the context's error, got %v", err)
	}
	// A partial file is worse than none: the next attempt would treat it as a
	// real file and skip it under "update if newer".
	if info, statErr := dst.Stat("/big.bin"); statErr == nil {
		t.Errorf("a cancelled copy left a %d byte file behind", info.Size())
	}
}

// Cancelling before the first byte is not an error to work around: nothing
// happens, and no file appears.
func TestCopyWithCancelledContext(t *testing.T) {
	src, dst, _, _ := pair(t)
	write(t, src, "/a.txt", "a")
	req := request(src, dst, "a.txt")
	plan, err := xfer.PlanCopy(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := xfer.Copy(ctx, req, plan); !errors.Is(err, context.Canceled) {
		t.Errorf("Copy with a cancelled context = %v, want context.Canceled", err)
	}
	if _, statErr := dst.Stat("/a.txt"); statErr == nil {
		t.Error("nothing should have been copied")
	}
}

// Errors are per item: one source that cannot be read does not stop the rest,
// and the result says which one failed.
func TestOneFailureDoesNotStopTheRest(t *testing.T) {
	src, dst, _, _ := pair(t)
	write(t, src, "/good.txt", "fine")
	write(t, src, "/vanishes.txt", "here for now")
	req := request(src, dst, "good.txt", "vanishes.txt")
	plan, err := xfer.PlanCopy(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	// The file disappears between the plan and the copy, which is what a busy
	// remote directory does.
	if err := src.Remove("/vanishes.txt"); err != nil {
		t.Fatal(err)
	}

	result, err := xfer.Copy(context.Background(), req, plan)
	if err == nil {
		t.Fatal("a failed item should be reported")
	}
	if result.Created != 1 {
		t.Errorf("the good file should still have been copied, result %+v", result)
	}
	if len(result.Errors) != 1 || result.Errors[0].Name != "vanishes.txt" {
		t.Errorf("the failure should name the item: %+v", result.Errors)
	}
	if got := read(t, dst, "/good.txt"); got != "fine" {
		t.Errorf("the good file holds %q", got)
	}
}

// Nothing to copy is a clear error, not a silent success.
func TestNothingToCopy(t *testing.T) {
	src, dst, _, _ := pair(t)
	if err := (xfer.Request{Src: src, Dst: dst}).Validate(); !errors.Is(err, xfer.ErrNothingToCopy) {
		t.Errorf("Validate = %v, want ErrNothingToCopy", err)
	}
}

// A plan's counts are what the interface summarises.
func TestPlanCounts(t *testing.T) {
	src, dst, _, _ := pair(t)
	write(t, src, "/a.txt", "a")
	write(t, src, "/b.txt", "b")
	write(t, dst, "/b.txt", "b")
	req := request(src, dst, "a.txt", "b.txt")
	req.Policy = xfer.PolicyUpdateIfNewer
	plan, err := xfer.PlanCopy(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	counts := plan.Counts()
	if counts[xfer.ActionCreate] != 1 || counts[xfer.ActionSkip] != 1 {
		t.Errorf("counts = %v", counts)
	}
}

// The source is never modified: a copy is a read.
func TestSourceIsUntouched(t *testing.T) {
	src, dst, srcDir, _ := pair(t)
	write(t, src, "/keep.txt", "body")
	before, err := os.Stat(filepath.Join(srcDir, "keep.txt"))
	if err != nil {
		t.Fatal(err)
	}
	req := request(src, dst, "keep.txt")
	plan, _ := xfer.PlanCopy(context.Background(), req)
	if _, err := xfer.Copy(context.Background(), req, plan); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(filepath.Join(srcDir, "keep.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) || after.Size() != before.Size() {
		t.Errorf("the source changed: %v -> %v", before.ModTime(), after.ModTime())
	}
}

// workers actually work: with a slow filesystem, four workers finish faster than
// one, and the result is identical either way.
func TestWorkersCopyInParallel(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	for i := 0; i < 8; i++ {
		if err := os.WriteFile(filepath.Join(src, fmt.Sprintf("f%d", i)), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	srcFS, err := vfs.NewLocal(src)
	if err != nil {
		t.Fatal(err)
	}
	dstFS, err := vfs.NewLocal(dst)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, 8)
	for i := 0; i < 8; i++ {
		names = append(names, fmt.Sprintf("f%d", i))
	}

	run := func(workers int) time.Duration {
		req := xfer.Request{
			Src: srcFS, SrcDir: "/", Dst: dstFS, DstDir: "/",
			Names: names, Policy: xfer.PolicyOverwrite, Workers: workers,
		}
		plan, err := xfer.PlanCopy(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		// A slow source makes the difference visible without a network.
		req.Src = slowFS{FS: srcFS, delay: 5 * time.Millisecond}
		start := time.Now()
		result, err := xfer.Copy(context.Background(), req, plan)
		if err != nil {
			t.Fatal(err)
		}
		// The first run creates, the second updates; both must move every file.
		if result.Created+result.Updated != len(names) {
			t.Errorf("workers %d: copied %d of %d", workers, result.Created+result.Updated, len(names))
		}
		return time.Since(start)
	}

	// Warm the cache with the sequential run first, then compare.
	sequential := run(1)
	parallel := run(4)
	if parallel >= sequential {
		t.Errorf("four workers (%v) were not faster than one (%v)", parallel, sequential)
	}
}

// A filesystem that can copy server-side is asked to, and the bytes are never
// read or written through this machine.
func TestCopyUsesServerSideCopy(t *testing.T) {
	srcDir, dstDir := t.TempDir(), t.TempDir()
	srcFS, err := vfs.NewLocal(srcDir)
	if err != nil {
		t.Fatal(err)
	}
	dstFS, err := vfs.NewLocal(dstDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "a.bin"), []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	copier := &remoteCopyFS{FS: dstFS}

	req := xfer.Request{Src: srcFS, SrcDir: "/", Dst: copier, DstDir: "/",
		Names: []string{"a.bin"}, Policy: xfer.PolicyOverwrite}
	plan, err := xfer.PlanCopy(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	result, err := xfer.Copy(context.Background(), req, plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(copier.calls) != 1 || copier.calls[0] != "/a.bin → /a.bin" {
		t.Fatalf("server-side copy calls = %v", copier.calls)
	}
	if result.Bytes != 10 {
		t.Errorf("bytes = %d, want 10", result.Bytes)
	}
	// The destination was never opened: the copy stayed inside the fake.
	if _, err := os.Stat(filepath.Join(dstDir, "a.bin")); err == nil {
		t.Error("the file should not have been streamed through the copy engine")
	}
}

// remoteCopyFS pretends to be object storage: it records the copy instead of
// moving bytes, so a streamed copy would be visible as a missing file.
type remoteCopyFS struct {
	vfs.FS
	calls []string
}

func (r *remoteCopyFS) CopyRemote(src vfs.FS, srcName, dstName string) error {
	r.calls = append(r.calls, srcName+" → "+dstName)
	return nil
}
