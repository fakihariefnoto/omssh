// Package archive reads and writes the archive formats people actually have on a
// server: zip, tar, and tar with gzip or bzip2 compression. It works on vfs, so
// "extract the tarball from the server into my local project" and "extract my
// local zip on the server" are the same call with the ends swapped.
//
// The formats are deliberately few. Each one is a promise that it will still work
// in five years against the archives real machines produce, and Go's standard
// library covers exactly these; a half-working rar or 7z would be worse than
// saying "use the shell".
package archive

import (
	"archive/tar"
	"archive/zip"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/fakihariefnoto/omssh/internal/vfs"
)

// Format is one of the archive formats omssh understands.
type Format int

// The formats. Order matters only for the help text.
const (
	Unknown Format = iota
	Zip
	Tar
	TarGz
	TarBz2
	Gz
)

// String names the format the way a person would.
func (f Format) String() string {
	switch f {
	case Zip:
		return "zip"
	case Tar:
		return "tar"
	case TarGz:
		return "tar.gz"
	case TarBz2:
		return "tar.bz2"
	case Gz:
		return "gz"
	default:
		return "unknown"
	}
}

// Extractable reports whether the format holds more than one file.
func (f Format) Extractable() bool { return f == Zip || f == Tar || f == TarGz || f == TarBz2 }

// Detect names the format from a file name. It looks at the end, longest suffix
// first, so "x.tar.gz" is not mistaken for a gzip of a plain file.
func Detect(name string) Format {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return Zip
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return TarGz
	case strings.HasSuffix(lower, ".tar.bz2"), strings.HasSuffix(lower, ".tbz2"), strings.HasSuffix(lower, ".tbz"):
		return TarBz2
	case strings.HasSuffix(lower, ".tar"):
		return Tar
	case strings.HasSuffix(lower, ".gz"):
		return Gz
	default:
		return Unknown
	}
}

// DefaultName suggests an archive name for a set of entries: a directory becomes
// "dir.tar.gz", a few files become "files.tar.gz", and a single file keeps its own
// name as a base.
func DefaultName(names []string) string {
	switch len(names) {
	case 0:
		return "archive.tar.gz"
	case 1:
		base := strings.TrimSuffix(names[0], "/")
		if format := Detect(base); format == Unknown {
			return base + ".tar.gz"
		}
		return base + ".tar.gz"
	default:
		return names[0] + "-and-more.tar.gz"
	}
}

// Stem is the name without its archive extension: "site.tar.gz" becomes "site",
// which is the folder an extraction should land in.
func Stem(name string) string {
	lower := strings.ToLower(name)
	for _, suffix := range []string{".tar.gz", ".tar.bz2", ".tgz", ".tbz2", ".tbz", ".tar", ".zip", ".gz"} {
		if strings.HasSuffix(lower, suffix) {
			return name[:len(name)-len(suffix)]
		}
	}
	return name
}

// Entry is one member of an archive.
type Entry struct {
	Name string
	Size int64
	Mode fs.FileMode
	Dir  bool
}

// Extract unpacks srcPath into dstDir on dst. It returns how many entries were
// written.
//
// Names are cleaned before use and anything that would escape dstDir is refused:
// an archive is input from somewhere else, and "somewhere else" can be hostile.
func Extract(ctx context.Context, src vfs.FS, srcPath string, dst vfs.FS, dstDir string, progress func(name string)) (int, error) {
	format := Detect(srcPath)
	if !format.Extractable() {
		return 0, fmt.Errorf("%s: %s is not an archive to extract", path.Base(srcPath), format)
	}
	file, err := src.Open(srcPath)
	if err != nil {
		return 0, err
	}
	defer func() { _ = file.Close() }()

	switch format {
	case Zip:
		return extractZip(ctx, src, srcPath, dst, dstDir, progress)
	default:
		reader, closeReader, err := tarReader(file, format)
		if err != nil {
			return 0, err
		}
		defer closeReader()
		return extractTar(ctx, reader, dst, dstDir, progress)
	}
}

// tarReader wraps the file in the decompressor the format asks for.
func tarReader(file io.Reader, format Format) (*tar.Reader, func(), error) {
	switch format {
	case Tar:
		return tar.NewReader(file), func() {}, nil
	case TarGz, Gz:
		zr, err := gzip.NewReader(file)
		if err != nil {
			return nil, nil, err
		}
		return tar.NewReader(zr), func() { _ = zr.Close() }, nil
	case TarBz2:
		return tar.NewReader(bzip2.NewReader(file)), func() {}, nil
	default:
		return nil, nil, fmt.Errorf("cannot read %s as a tar archive", format)
	}
}

func extractTar(ctx context.Context, tr *tar.Reader, dst vfs.FS, dstDir string, progress func(string)) (int, error) {
	written := 0
	for {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return written, nil
		}
		if err != nil {
			return written, err
		}
		name, err := safeName(header.Name)
		if err != nil {
			return written, err
		}
		target := dst.Join(dstDir, name)

		switch header.Typeflag {
		case tar.TypeDir:
			if err := dst.MkdirAll(target, modeOf(header.Mode)|0o700); err != nil {
				return written, err
			}
		case tar.TypeReg:
			if err := dst.MkdirAll(dst.Dir(target), 0o755); err != nil {
				return written, err
			}
			if err := writeFile(dst, target, tr, modeOf(header.Mode)); err != nil {
				return written, err
			}
			written++
			if progress != nil {
				progress(name)
			}
		case tar.TypeSymlink:
			// A link is only safe if its target stays inside the archive: a link
			// to /etc/passwd written into a directory is an escape even without a
			// single ".." in the entry name.
			if _, err := safeName(header.Linkname); err != nil {
				return written, fmt.Errorf("%s: link %q points outside the archive", name, header.Linkname)
			}
			if err := dst.MkdirAll(dst.Dir(target), 0o755); err != nil {
				return written, err
			}
			if err := dst.Symlink(header.Linkname, target); err != nil {
				// A filesystem without symlinks should not lose the file: write
				// the target as a small text file instead, and say so.
				if err := writeFile(dst, target, strings.NewReader(header.Linkname+"\n"), 0o644); err != nil {
					return written, err
				}
			}
			written++
			if progress != nil {
				progress(name)
			}
		case tar.TypeLink, tar.TypeXGlobalHeader, tar.TypeXHeader, tar.TypeGNULongName, tar.TypeGNULongLink:
			// Metadata headers are consumed by the tar reader itself; hard links
			// are rare enough that a copy of the target is not worth the risk of
			// writing to the wrong place.
			continue
		default:
			// Devices, fifos and sockets: not ours to create.
			continue
		}
	}
}

func extractZip(ctx context.Context, src vfs.FS, srcPath string, dst vfs.FS, dstDir string, progress func(string)) (int, error) {
	// zip has its index at the end, so it needs random access. A vfs is a stream,
	// so the archive is spooled to a temporary file first: one extra copy, in
	// exchange for the format working over SFTP at all.
	tmp, cleanup, err := spool(src, srcPath)
	if err != nil {
		return 0, err
	}
	defer cleanup()

	reader, err := zip.OpenReader(tmp)
	if err != nil {
		return 0, err
	}
	defer func() { _ = reader.Close() }()

	written := 0
	for _, member := range reader.File {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		name, err := safeName(member.Name)
		if err != nil {
			return written, err
		}
		target := dst.Join(dstDir, name)
		if member.FileInfo().IsDir() {
			if err := dst.MkdirAll(target, member.Mode()|0o700); err != nil {
				return written, err
			}
			continue
		}
		body, err := member.Open()
		if err != nil {
			return written, err
		}
		if err := dst.MkdirAll(dst.Dir(target), 0o755); err != nil {
			_ = body.Close()
			return written, err
		}
		// Mode 0 happens on archives written on systems without Unix modes.
		mode := member.Mode()
		if mode == 0 {
			mode = 0o644
		}
		writeErr := writeFile(dst, target, body, mode)
		_ = body.Close()
		if writeErr != nil {
			return written, writeErr
		}
		written++
		if progress != nil {
			progress(name)
		}
	}
	return written, nil
}

// spool writes a vfs file to a temporary local file, because zip needs random
// access and vfs is a stream.
func spool(src vfs.FS, srcPath string) (tmpPath string, cleanup func(), err error) {
	file, err := src.Open(srcPath)
	if err != nil {
		return "", func() {}, err
	}
	defer func() { _ = file.Close() }()

	tmp, err := os.CreateTemp("", "omssh-archive-*")
	if err != nil {
		return "", func() {}, err
	}
	cleanup = func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}
	if _, err := io.Copy(tmp, file); err != nil {
		cleanup()
		return "", func() {}, err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return tmp.Name(), cleanup, nil
}

// modeOf reads the permission bits out of a tar header. The header stores them as
// an int64 and FileMode is uint32; the mask is what keeps the conversion honest.
func modeOf(mode int64) fs.FileMode {
	return fs.FileMode(mode & 0o7777) // the mask keeps the conversion inside uint32
}

// safeName turns an archive member name into a relative path inside the
// destination, refusing absolute paths and any "..".
func safeName(name string) (string, error) {
	clean := path.Clean(strings.ReplaceAll(name, "\\", "/"))
	if clean == "." || clean == "" {
		return "", errors.New("archive contains an empty name")
	}
	if path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("archive entry %q escapes the destination", name)
	}
	return clean, nil
}

// writeFile creates one file and copies the reader into it.
func writeFile(dst vfs.FS, target string, body io.Reader, mode fs.FileMode) error {
	if mode == 0 {
		mode = 0o644
	}
	file, err := dst.OpenFile(target, vfs.O_WRONLY|vfs.O_CREATE|vfs.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(file, body); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

// Item is one thing to put in a new archive.
type Item struct {
	FS   vfs.FS
	Dir  string // the directory the name is in
	Name string
}

// Compress writes items into dstPath on dst. The format follows the file name, so
// "backup.tar.gz" means what it says; an unknown extension is refused rather than
// guessed at.
func Compress(ctx context.Context, dst vfs.FS, dstPath string, items []Item, progress func(name string)) (int, error) {
	format := Detect(dstPath)
	if format == Unknown {
		return 0, fmt.Errorf("%s: name the format (like .tar.gz or .zip)", path.Base(dstPath))
	}
	if err := dst.MkdirAll(dst.Dir(dstPath), 0o755); err != nil {
		return 0, err
	}
	file, err := dst.OpenFile(dstPath, vfs.O_WRONLY|vfs.O_CREATE|vfs.O_TRUNC, 0o644)
	if err != nil {
		return 0, err
	}

	var written int
	switch format {
	case Zip:
		written, err = compressZip(ctx, file, items, progress)
	case Tar, TarGz, TarBz2:
		written, err = compressTar(ctx, file, format, items, progress)
	case Gz:
		written, err = 0, errors.New("a .gz holds one file; compress a folder instead")
	case Unknown:
		written, err = 0, errors.New("name the archive format")
	}
	if err != nil {
		_ = file.Close()
		_ = dst.Remove(dstPath)
		return written, err
	}
	if err := file.Close(); err != nil {
		return written, err
	}
	return written, nil
}

func compressTar(ctx context.Context, out io.Writer, format Format, items []Item, progress func(string)) (int, error) {
	var zw *gzip.Writer
	if format == TarGz {
		zw = gzip.NewWriter(out)
		defer func() { _ = zw.Close() }()
		out = zw
	}
	tw := tar.NewWriter(out)
	defer func() { _ = tw.Close() }()

	written := 0
	for _, item := range items {
		root := item.FS.Join(item.Dir, item.Name)
		err := walkItem(ctx, item.FS, root, item.Name, func(name string, info fs.FileInfo, body io.Reader) error {
			header := &tar.Header{
				Name:    name,
				Mode:    int64(info.Mode().Perm()),
				ModTime: info.ModTime(),
			}
			if info.IsDir() {
				header.Typeflag = tar.TypeDir
				header.Name += "/"
			} else {
				header.Typeflag = tar.TypeReg
				header.Size = info.Size()
			}
			if err := tw.WriteHeader(header); err != nil {
				return err
			}
			if body != nil {
				if _, err := io.Copy(tw, body); err != nil {
					return err
				}
			}
			if progress != nil {
				progress(name)
			}
			written++
			return nil
		})
		if err != nil {
			return written, err
		}
	}
	return written, tw.Close()
}

func compressZip(ctx context.Context, out io.Writer, items []Item, progress func(string)) (int, error) {
	zw := zip.NewWriter(out)
	written := 0
	for _, item := range items {
		root := item.FS.Join(item.Dir, item.Name)
		err := walkItem(ctx, item.FS, root, item.Name, func(name string, info fs.FileInfo, body io.Reader) error {
			header := &zip.FileHeader{Name: name, Modified: info.ModTime(), Method: zip.Deflate}
			header.SetMode(info.Mode())
			writer, err := zw.CreateHeader(header)
			if err != nil {
				return err
			}
			if body != nil {
				if _, err := io.Copy(writer, body); err != nil {
					return err
				}
			}
			if progress != nil {
				progress(name)
			}
			written++
			return nil
		})
		if err != nil {
			return written, err
		}
	}
	return written, zw.Close()
}

// walkItem visits one item, root first, naming each visited thing relative to the
// archive. Bodies are opened for regular files only; directories yield nil.
func walkItem(ctx context.Context, fsys vfs.FS, root, archiveName string, visit func(name string, info fs.FileInfo, body io.Reader) error) error {
	info, err := fsys.Lstat(root)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !info.IsDir() {
		file, err := fsys.Open(root)
		if err != nil {
			return err
		}
		defer func() { _ = file.Close() }()
		return visit(archiveName, info, file)
	}
	if err := visit(archiveName, info, nil); err != nil {
		return err
	}
	entries, err := fsys.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		child := fsys.Join(root, entry.Name())
		childName := archiveName + "/" + entry.Name()
		if err := walkItem(ctx, fsys, child, childName, visit); err != nil {
			return err
		}
	}
	return nil
}

// List reports what an archive contains without writing anything. It is what the
// preview pane shows for an archive, and what "extract" can count first.
func List(src vfs.FS, srcPath string) ([]Entry, error) {
	format := Detect(srcPath)
	if !format.Extractable() {
		return nil, fmt.Errorf("%s: %s cannot be listed", path.Base(srcPath), format)
	}
	file, err := src.Open(srcPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	if format == Zip {
		tmp, cleanup, err := spool(src, srcPath)
		if err != nil {
			return nil, err
		}
		defer cleanup()
		reader, err := zip.OpenReader(tmp)
		if err != nil {
			return nil, err
		}
		defer func() { _ = reader.Close() }()
		out := make([]Entry, 0, len(reader.File))
		for _, member := range reader.File {
			info := member.FileInfo()
			out = append(out, Entry{Name: member.Name, Size: info.Size(), Mode: info.Mode(), Dir: info.IsDir()})
		}
		return out, nil
	}
	tr, closeReader, err := tarReader(file, format)
	if err != nil {
		return nil, err
	}
	defer closeReader()

	var out []Entry
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return out, err
		}
		out = append(out, Entry{
			Name: header.Name,
			Size: header.Size,
			Mode: modeOf(header.Mode),
			Dir:  header.Typeflag == tar.TypeDir,
		})
	}
}
