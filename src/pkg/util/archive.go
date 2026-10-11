package util

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ArchiveFormats are the formats WriteArchive writes.
var ArchiveFormats = []string{"tar", "tar.gz", "zip"}

// archiveTime is every entry's modification time, so the same tree always gives the same bytes:
// staging copies get a new mtime on every build.
var archiveTime = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// WriteArchive packs the tree under dir into out, as a tar, tar.gz or zip archive. Entry names
// are paths relative to dir with forward slashes, without a "./" prefix; directories end in "/".
// The archive is deterministic: entries come in the order of their names, directory by directory,
// with a fixed modification time, owner 0 and the files' permission bits. With followSymlinks a
// symlink is stored as what it points to (a directory is walked), otherwise as a link.
func WriteArchive(dir, out, format string, followSymlinks bool) (err error) {
	if rel, relErr := filepath.Rel(dir, out); relErr == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("archive %s would be inside the directory it packs, %s", out, dir)
	}
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
	}()

	var w archiveWriter
	switch format {
	case "tar":
		w = &tarArchive{tw: tar.NewWriter(f)}
	case "tar.gz":
		gz := gzip.NewWriter(f)
		w = &tarArchive{tw: tar.NewWriter(gz), gz: gz}
	case "zip":
		w = &zipArchive{zw: zip.NewWriter(f)}
	default:
		return fmt.Errorf("unknown archive format %q (one of: %s)", format, strings.Join(ArchiveFormats, ", "))
	}
	a := &archiver{w: w, follow: followSymlinks, active: map[string]bool{}}
	if err := a.addChildren(dir, ""); err != nil {
		return err
	}
	return w.Close()
}

// archiveWriter writes the entries of one archive format.
type archiveWriter interface {
	dir(name string, mode os.FileMode) error
	file(name string, mode os.FileMode, size int64, content io.Reader) error
	symlink(name, target string) error
	Close() error
}

type archiver struct {
	w      archiveWriter
	follow bool
	active map[string]bool // directories being walked, by real path: a followed link back to one is a loop
}

// addChildren adds the entries of directory path, whose entry name is name ("" for the root).
func (a *archiver) addChildren(path, name string) error {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	if a.active[real] {
		return fmt.Errorf("symlink loop: %s leads back to %s", path, real)
	}
	a.active[real] = true
	defer delete(a.active, real)

	entries, err := os.ReadDir(path) // sorted by name
	if err != nil {
		return err
	}
	for _, e := range entries {
		child := e.Name()
		if name != "" {
			child = name + "/" + child
		}
		if err := a.add(filepath.Join(path, e.Name()), child); err != nil {
			return err
		}
	}
	return nil
}

// add adds the file, directory or symlink at path as the entry name.
func (a *archiver) add(path, name string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		if !a.follow {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return a.w.symlink(name, target)
		}
		if info, err = os.Stat(path); err != nil {
			return fmt.Errorf("following symlink %s: %w", path, err)
		}
	}
	switch {
	case info.IsDir():
		if err := a.w.dir(name+"/", info.Mode().Perm()); err != nil {
			return err
		}
		return a.addChildren(path, name)
	case info.Mode().IsRegular():
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		return a.w.file(name, info.Mode().Perm(), info.Size(), f)
	}
	return fmt.Errorf("%s is not a file, a directory or a symlink (%s)", path, info.Mode().Type())
}

type tarArchive struct {
	tw *tar.Writer
	gz *gzip.Writer // nil for a plain tar
}

func (t *tarArchive) header(name string, mode os.FileMode, typ byte) *tar.Header {
	return &tar.Header{Name: name, Mode: int64(mode), Typeflag: typ, ModTime: archiveTime}
}

func (t *tarArchive) dir(name string, mode os.FileMode) error {
	return t.tw.WriteHeader(t.header(name, mode, tar.TypeDir))
}

func (t *tarArchive) file(name string, mode os.FileMode, size int64, content io.Reader) error {
	h := t.header(name, mode, tar.TypeReg)
	h.Size = size
	if err := t.tw.WriteHeader(h); err != nil {
		return err
	}
	_, err := io.Copy(t.tw, content)
	return err
}

func (t *tarArchive) symlink(name, target string) error {
	h := t.header(name, 0o777, tar.TypeSymlink)
	h.Linkname = target
	return t.tw.WriteHeader(h)
}

func (t *tarArchive) Close() error {
	if err := t.tw.Close(); err != nil {
		return err
	}
	if t.gz != nil {
		return t.gz.Close()
	}
	return nil
}

type zipArchive struct {
	zw *zip.Writer
}

func (z *zipArchive) create(name string, mode os.FileMode, method uint16) (io.Writer, error) {
	h := &zip.FileHeader{Name: name, Method: method, Modified: archiveTime}
	h.SetMode(mode)
	return z.zw.CreateHeader(h)
}

func (z *zipArchive) dir(name string, mode os.FileMode) error {
	_, err := z.create(name, mode|os.ModeDir, zip.Store)
	return err
}

func (z *zipArchive) file(name string, mode os.FileMode, size int64, content io.Reader) error {
	w, err := z.create(name, mode, zip.Deflate)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, content)
	return err
}

// symlink stores a link the way Info-ZIP does: the target is the content, the mode says link.
func (z *zipArchive) symlink(name, target string) error {
	w, err := z.create(name, os.ModeSymlink|0o777, zip.Store)
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, target)
	return err
}

func (z *zipArchive) Close() error {
	return z.zw.Close()
}
