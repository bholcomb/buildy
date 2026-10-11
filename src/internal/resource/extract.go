package resource

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// extractArchive extracts the archive at archivePath into dest. The format comes from
// formatName's extension (archivePath may carry a cache prefix).
//
// The archive is extracted into a new temporary directory next to dest, opened as an os.Root so
// that no entry can create or write anything outside it, and the directory is renamed to dest
// only when every entry was extracted. An interrupted or failed extraction never leaves a
// partial dest behind. If every entry sits under one top-level directory ("project-1.0/"), that
// directory is stripped.
func extractArchive(archivePath, formatName, dest string) error {
	format, err := archiveFormat(formatName)
	if err != nil {
		return err
	}

	parent := filepath.Dir(dest)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("failed to create %s: %w", parent, err)
	}
	removeStaleExtractions(dest)
	tmp, err := os.MkdirTemp(parent, extractTempPrefix(dest))
	if err != nil {
		return fmt.Errorf("failed to create a temporary extraction directory: %w", err)
	}

	if err := extractInto(archivePath, format, tmp); err != nil {
		os.RemoveAll(tmp)
		return fmt.Errorf("extracting %s: %w", formatName, err)
	}

	if err := os.RemoveAll(dest); err != nil {
		os.RemoveAll(tmp)
		return fmt.Errorf("failed to remove the previous extraction %s: %w", dest, err)
	}
	if err := os.Rename(tmp, dest); err != nil {
		os.RemoveAll(tmp)
		return fmt.Errorf("failed to move the extraction into place at %s: %w", dest, err)
	}
	return nil
}

// extractTempPrefix is the name prefix of dest's temporary extraction directories.
func extractTempPrefix(dest string) string {
	return "." + filepath.Base(dest) + ".extract-"
}

// removeStaleExtractions removes temporary directories left by an interrupted extraction of dest.
func removeStaleExtractions(dest string) {
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(dest), extractTempPrefix(dest)+"*"))
	for _, m := range matches {
		os.RemoveAll(m)
	}
}

// archiveFormat returns "zip", "tar" or "tar.gz" for an archive file name.
func archiveFormat(name string) (string, error) {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return "tar.gz", nil
	case strings.HasSuffix(lower, ".tar"):
		return "tar", nil
	case strings.HasSuffix(lower, ".zip"):
		return "zip", nil
	}
	return "", fmt.Errorf("unsupported archive format: %s (expected .zip, .tar, .tar.gz or .tgz)", name)
}

// archiveEntry is one entry of an archive, in a form shared by zip and tar.
type archiveEntry struct {
	name     string      // name in the archive
	kind     byte        // 'd' directory, 'f' file, 's' symlink, 'h' hard link
	mode     fs.FileMode // permission bits
	linkname string      // symlink target, or hard link source (an archive name)
	open     func() (io.ReadCloser, error)
}

// entrySource reads an archive's entries in order. It is called twice: once to find the common
// prefix, once to extract.
type entrySource func(visit func(archiveEntry) error) error

func extractInto(archivePath, format, dir string) error {
	var source entrySource
	switch format {
	case "zip":
		r, err := zip.OpenReader(archivePath)
		if err != nil {
			return err
		}
		defer r.Close()
		source = zipEntries(&r.Reader)
	case "tar", "tar.gz":
		source = tarEntries(archivePath, format == "tar.gz")
	}

	prefix, err := commonPrefix(source)
	if err != nil {
		return err
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()

	x := &extractor{root: root, prefix: prefix}
	return source(x.extract)
}

func zipEntries(r *zip.Reader) entrySource {
	return func(visit func(archiveEntry) error) error {
		for _, f := range r.File {
			e := archiveEntry{name: f.Name, mode: f.Mode().Perm(), open: f.Open}
			switch {
			case f.Mode().IsDir() || strings.HasSuffix(f.Name, "/"):
				e.kind = 'd'
			case f.Mode()&fs.ModeSymlink != 0:
				e.kind = 's'
				target, err := readSmall(f.Open)
				if err != nil {
					return fmt.Errorf("entry %q: %w", f.Name, err)
				}
				e.linkname = target
			case f.Mode().IsRegular():
				e.kind = 'f'
			default:
				return fmt.Errorf("entry %q: unsupported file type %v", f.Name, f.Mode().Type())
			}
			if err := visit(e); err != nil {
				return err
			}
		}
		return nil
	}
}

func readSmall(open func() (io.ReadCloser, error)) (string, error) {
	rc, err := open()
	if err != nil {
		return "", err
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, 4096))
	return string(data), err
}

func tarEntries(archivePath string, gzipped bool) entrySource {
	return func(visit func(archiveEntry) error) error {
		file, err := os.Open(archivePath)
		if err != nil {
			return err
		}
		defer file.Close()
		var r io.Reader = file
		if gzipped {
			gz, err := gzip.NewReader(file)
			if err != nil {
				return err
			}
			defer gz.Close()
			r = gz
		}
		tr := tar.NewReader(r)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			e := archiveEntry{
				name:     h.Name,
				mode:     fs.FileMode(h.Mode).Perm(),
				linkname: h.Linkname,
				open:     func() (io.ReadCloser, error) { return io.NopCloser(tr), nil },
			}
			switch h.Typeflag {
			case tar.TypeDir:
				e.kind = 'd'
			case tar.TypeReg:
				e.kind = 'f'
			case tar.TypeSymlink:
				e.kind = 's'
			case tar.TypeLink:
				e.kind = 'h'
			case tar.TypeXGlobalHeader:
				// pax_global_header (git archive): metadata, not a file.
				continue
			default:
				return fmt.Errorf("entry %q: unsupported tar entry type %q", h.Name, h.Typeflag)
			}
			if err := visit(e); err != nil {
				return err
			}
		}
	}
}

// cleanEntryName checks an archive name and returns it cleaned, with forward slashes. Absolute
// names, volume names and ".." components are rejected rather than cleaned away.
func cleanEntryName(name string) (string, error) {
	slashed := strings.ReplaceAll(name, "\\", "/")
	if strings.HasPrefix(slashed, "/") || filepath.VolumeName(name) != "" ||
		(len(slashed) >= 2 && slashed[1] == ':') {
		return "", fmt.Errorf("entry %q has an absolute path", name)
	}
	for _, part := range strings.Split(slashed, "/") {
		if part == ".." {
			return "", fmt.Errorf("entry %q has a '..' component", name)
		}
	}
	return path.Clean(slashed), nil
}

// commonPrefix returns "top/" if every entry is top/ or under it, else "".
func commonPrefix(source entrySource) (string, error) {
	top := ""
	shared := true
	first := true
	err := source(func(e archiveEntry) error {
		name, err := cleanEntryName(e.name)
		if err != nil || name == "." {
			return err
		}
		head, _, nested := strings.Cut(name, "/")
		if !nested && e.kind != 'd' {
			shared = false // a file at the top level
		}
		if first {
			top, first = head, false
		} else if head != top {
			shared = false
		}
		return nil
	})
	if err != nil || !shared || first {
		return "", err
	}
	return top + "/", nil
}

// extractor writes entries beneath root, a fresh directory the archive is the only writer of.
type extractor struct {
	root   *os.Root
	prefix string
}

// target returns the root-relative path for an archive name, "" for the stripped prefix itself.
func (x *extractor) target(name string) (string, error) {
	clean, err := cleanEntryName(name)
	if err != nil {
		return "", err
	}
	if clean+"/" == x.prefix || clean == "." {
		return "", nil
	}
	return strings.TrimPrefix(clean, x.prefix), nil
}

func (x *extractor) extract(e archiveEntry) error {
	name, err := x.target(e.name)
	if err != nil || name == "" {
		return err
	}
	if e.kind == 'd' {
		if err := x.root.MkdirAll(name, 0o755); err != nil {
			return fmt.Errorf("entry %q: %w", e.name, err)
		}
		return nil
	}

	if dir := path.Dir(name); dir != "." {
		if err := x.root.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("entry %q: %w", e.name, err)
		}
	}
	// A duplicate entry replaces the earlier one; whatever is there, this archive created it.
	if _, err := x.root.Lstat(name); err == nil {
		if err := x.root.RemoveAll(name); err != nil {
			return fmt.Errorf("entry %q: %w", e.name, err)
		}
	}

	switch e.kind {
	case 'f':
		return x.file(e, name)
	case 's':
		target := strings.ReplaceAll(e.linkname, "\\", "/")
		resolved := path.Join(path.Dir(name), target)
		if path.IsAbs(target) || resolved == ".." || strings.HasPrefix(resolved, "../") {
			return fmt.Errorf("entry %q: symlink target %q leaves the extraction directory", e.name, e.linkname)
		}
		if err := x.root.Symlink(target, name); err != nil {
			return fmt.Errorf("entry %q: %w", e.name, err)
		}
	case 'h':
		source, err := x.target(e.linkname)
		if err != nil {
			return fmt.Errorf("entry %q: hard link source: %w", e.name, err)
		}
		if source == "" {
			return fmt.Errorf("entry %q: hard link to a directory", e.name)
		}
		if err := x.root.Link(source, name); err != nil {
			return fmt.Errorf("entry %q: %w", e.name, err)
		}
	}
	return nil
}

func (x *extractor) file(e archiveEntry, name string) error {
	// O_EXCL: the write never goes through an existing file or symlink.
	out, err := x.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("entry %q: %w", e.name, err)
	}
	in, err := e.open()
	if err != nil {
		out.Close()
		return fmt.Errorf("entry %q: %w", e.name, err)
	}
	_, copyErr := io.Copy(out, in)
	in.Close()
	if err := out.Close(); copyErr == nil {
		copyErr = err
	}
	if copyErr != nil {
		return fmt.Errorf("entry %q: %w", e.name, copyErr)
	}
	// Permission bits only: no setuid, setgid or sticky bit. Archives that record no
	// permissions (some zip tools) get rw-r--r--.
	mode := e.mode & 0o777
	if mode == 0 {
		mode = 0o644
	}
	if err := x.root.Chmod(name, mode); err != nil {
		return fmt.Errorf("entry %q: %w", e.name, err)
	}
	return nil
}
