package archive

import (
	"archive/tar"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fakihariefnoto/omssh/internal/vfs"
)

// build makes a small tree to archive: a folder, a file inside it, and a file
// beside it.
func build(t *testing.T) (*vfs.Local, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "proj", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string]string{
		"proj/index.html": "<html>",
		"proj/sub/app.js": "console.log(1)",
		"notes.txt":       "hello",
	} {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fsys, err := vfs.NewLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	return fsys, dir
}

// The whole promise in one test: compress a tree, extract it somewhere else, and
// find the same shape and bytes.
func TestRoundTripTarballs(t *testing.T) {
	for _, format := range []struct {
		name string
		want Format
	}{
		{"bundle.tar.gz", TarGz},
		{"bundle.tar", Tar},
		{"bundle.zip", Zip},
	} {
		t.Run(format.name, func(t *testing.T) {
			src, srcDir := build(t)
			if got := Detect(format.name); got != format.want {
				t.Fatalf("Detect(%q) = %v, want %v", format.name, got, format.want)
			}

			items := []Item{
				{FS: src, Dir: "/", Name: "proj"},
				{FS: src, Dir: "/", Name: "notes.txt"},
			}
			if _, err := Compress(context.Background(), src, "/"+format.name, items, nil); err != nil {
				t.Fatal(err)
			}
			if info, err := os.Stat(filepath.Join(srcDir, format.name)); err != nil || info.Size() == 0 {
				t.Fatalf("the archive should exist and hold bytes: %v", err)
			}

			outDir := t.TempDir()
			dst, err := vfs.NewLocal(outDir)
			if err != nil {
				t.Fatal(err)
			}
			count, err := Extract(context.Background(), src, "/"+format.name, dst, "/", nil)
			if err != nil {
				t.Fatal(err)
			}
			if count != 3 {
				t.Errorf("expected three files to be written, got %d", count)
			}
			for path, want := range map[string]string{
				"proj/index.html": "<html>",
				"proj/sub/app.js": "console.log(1)",
				"notes.txt":       "hello",
			} {
				got, err := os.ReadFile(filepath.Join(outDir, path))
				if err != nil {
					t.Fatalf("%s should have been extracted: %v", path, err)
				}
				if string(got) != want {
					t.Errorf("%s = %q, want %q", path, got, want)
				}
			}
		})
	}
}

// An archive is input from elsewhere, so a member that climbs out of the
// destination must stop the whole extraction.
func TestExtractRefusesPathTraversal(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	body := []byte("owned")
	if err := tw.WriteHeader(&tar.Header{Name: "../../etc/passwd", Mode: 0o644, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "evil.tar"), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	src, err := vfs.NewLocal(srcDir)
	if err != nil {
		t.Fatal(err)
	}
	outDir := t.TempDir()
	dst, err := vfs.NewLocal(outDir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Extract(context.Background(), src, "/evil.tar", dst, "/", nil)
	if err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("path traversal should be refused, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(outDir, "..", "..", "etc", "passwd")); statErr == nil {
		t.Error("nothing may be written outside the destination")
	}
}

// Listing answers "what is in this" without writing anything, which is what the
// preview needs.
func TestListNamesTheMembers(t *testing.T) {
	src, _ := build(t)
	items := []Item{{FS: src, Dir: "/", Name: "proj"}}
	if _, err := Compress(context.Background(), src, "/list.tar.gz", items, nil); err != nil {
		t.Fatal(err)
	}
	entries, err := List(src, "/list.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	joined := strings.Join(names, ",")
	for _, want := range []string{"proj/", "proj/index.html", "proj/sub/app.js"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the listing should contain %q, got %q", want, joined)
		}
	}
}

// A name without a known extension is refused rather than guessed: an archive
// with the wrong contents is worse than an error.
func TestCompressNeedsAKnownFormat(t *testing.T) {
	src, _ := build(t)
	_, err := Compress(context.Background(), src, "/backup", []Item{{FS: src, Dir: "/", Name: "notes.txt"}}, nil)
	if err == nil {
		t.Fatal("an unknown extension should be refused")
	}
}
