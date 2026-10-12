package resource

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"buildy/pkg/util"
)

// DownloadMetadata stores information about a downloaded file
type DownloadMetadata struct {
	URL          string    `json:"url"`
	Checksum     string    `json:"checksum,omitempty"`
	SHA256       string    `json:"sha256"`
	Size         int64     `json:"size"`
	DownloadedAt time.Time `json:"downloaded_at"`
	Filename     string    `json:"filename"`
}

// FetchManager handles downloading and caching of external dependencies
type FetchManager struct {
	cacheDir string
}

// NewFetchManager creates a new FetchManager
func NewFetchManager(cacheDir string) *FetchManager {
	return &FetchManager{
		cacheDir: cacheDir,
	}
}

// git runs git with args in dir. Like a task's command it runs in its own process group, killed
// when ctx is done (an interrupt).
func git(ctx context.Context, dir string, args ...string) (string, error) {
	result, err := util.Run(ctx, util.ExecSpec{}, util.Command{Args: append([]string{"git"}, args...), Dir: dir}, dir, nil, 0)
	if err != nil {
		return "", fmt.Errorf("git %s: %w", args[0], err)
	}
	if result.ReturnCode != 0 {
		return result.Output, fmt.Errorf("git %s failed: %s", args[0], result.Output)
	}
	return result.Output, nil
}

// gitMarker records what a git checkout under deps/ was fetched for: the repository, the ref the
// dependency names, and the commit that ref resolved to.
type gitMarker struct {
	URL    string `json:"url"`
	Ref    string `json:"ref"`
	Commit string `json:"commit"`
}

// gitMarkerPath is the marker file next to a git checkout.
func gitMarkerPath(dest string) string {
	return dest + ".git.json"
}

func readGitMarker(dest string) (gitMarker, bool) {
	var m gitMarker
	data, err := os.ReadFile(gitMarkerPath(dest))
	if err != nil || json.Unmarshal(data, &m) != nil || m.Commit == "" {
		return gitMarker{}, false
	}
	return m, true
}

// isCommitID reports whether ref is a full commit ID (SHA-1 or SHA-256), which never moves.
func isCommitID(ref string) bool {
	if len(ref) != 40 && len(ref) != 64 {
		return false
	}
	for _, c := range ref {
		if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return false
		}
	}
	return true
}

// FetchedGit returns the commit dest holds for url at ref, as recorded when it was fetched, or ""
// if dest doesn't hold that checkout. It runs nothing.
func (fm *FetchManager) FetchedGit(url, ref, dest string) string {
	if m, ok := readGitMarker(dest); ok && m.URL == url && m.Ref == ref {
		return m.Commit
	}
	return ""
}

// FetchGit makes dest a checkout of ref (a branch, a tag, a full or abbreviated commit ID; empty:
// the default branch) of the repository at url, and returns its commit. The checkout is
// detached at the commit the ref resolved to.
//
// Once fetched, dest is used as it is, without the network, while the dependency names the same
// url and ref: a build is reproducible and works offline. update (--update-deps) fetches a ref
// that can move (a branch or a tag) again. A changed ref is fetched into the existing clone; a
// changed url starts a new clone.
func (fm *FetchManager) FetchGit(ctx context.Context, url, ref, dest string, update bool) (string, error) {
	if strings.HasPrefix(ref, "-") {
		return "", fmt.Errorf("the git ref %q starts with '-'", ref)
	}
	marker, ok := readGitMarker(dest)
	if ok && marker.URL == url && marker.Ref == ref && (!update || isCommitID(ref)) {
		head, err := git(ctx, dest, "rev-parse", "HEAD")
		if err == nil && strings.TrimSpace(head) == marker.Commit {
			return marker.Commit, nil
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		util.LogWarning("%s is not at commit %s any more: checking it out again", dest, marker.Commit)
	}

	// Drop the marker first: if this run dies part way, dest is not taken for a checkout of ref
	if err := os.Remove(gitMarkerPath(dest)); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if !ok || marker.URL != url || !fileExists(filepath.Join(dest, ".git")) {
		// Another repository, or none: start a new clone
		if err := os.RemoveAll(dest); err != nil {
			return "", fmt.Errorf("removing the previous checkout %s: %w", dest, err)
		}
		if err := os.MkdirAll(dest, 0o755); err != nil {
			return "", err
		}
		if _, err := git(ctx, dest, "init", "--quiet"); err != nil {
			return "", err
		}
		if _, err := git(ctx, dest, "remote", "add", "origin", url); err != nil {
			return "", err
		}
	}

	util.LogProgress("Fetching git: %s @ %s -> %s", url, refName(ref), dest)
	commit, err := fetchGitRef(ctx, dest, ref)
	if err != nil {
		return "", fmt.Errorf("fetching %s from %s: %w", refName(ref), url, err)
	}
	if _, err := git(ctx, dest, "checkout", "--quiet", "--force", "--detach", commit); err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(gitMarker{URL: url, Ref: ref, Commit: commit}, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(gitMarkerPath(dest), data, 0o644); err != nil {
		return "", fmt.Errorf("recording the checkout of %s: %w", dest, err)
	}
	return commit, nil
}

// refName names a ref for messages.
func refName(ref string) string {
	if ref == "" {
		return "the default branch"
	}
	return ref
}

// fetchGitRef fetches ref from origin into the repository in dir and returns its commit. A
// branch, a tag or a full commit ID is fetched alone, without history; anything else (an
// abbreviated commit ID) needs the whole history to be found in.
func fetchGitRef(ctx context.Context, dir, ref string) (string, error) {
	want := ref
	if want == "" {
		want = "HEAD"
	}
	_, shallowErr := git(ctx, dir, "fetch", "--quiet", "--depth", "1", "--no-tags", "origin", want)
	if shallowErr == nil {
		commit, err := git(ctx, dir, "rev-parse", "--verify", "FETCH_HEAD^{commit}")
		return strings.TrimSpace(commit), err
	}
	if ctx.Err() != nil || ref == "" {
		return "", shallowErr
	}
	args := []string{"fetch", "--quiet", "--tags", "origin", "+refs/heads/*:refs/remotes/origin/*"}
	if shallow, err := git(ctx, dir, "rev-parse", "--is-shallow-repository"); err == nil && strings.TrimSpace(shallow) == "true" {
		args = append(args[:2], append([]string{"--unshallow"}, args[2:]...)...)
	}
	if _, err := git(ctx, dir, args...); err != nil {
		return "", fmt.Errorf("%v; then fetching the whole history: %w", shallowErr, err)
	}
	commit, err := git(ctx, dir, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("no branch, tag or commit is named %q (%v)", ref, shallowErr)
	}
	return strings.TrimSpace(commit), nil
}

// GetGitCommit returns the current commit hash for a git repo
func (fm *FetchManager) GetGitCommit(dest string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dest
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get git commit: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

// FetchURL downloads an archive from a URL, extracts it into dest, and returns the archive's
// SHA-256.
//
// Downloads are cached under downloads/ by a hash of the full URL (plus its basename, for
// readability), so two URLs with the same file name never share a download. The extraction is
// recorded in dest's marker file against the URL and the archive's SHA-256; dest is reused only
// when the marker matches, so a partial or stale extraction is never accepted. update
// (--update-deps) downloads an archive without a checksum again: its content may have changed.
func (fm *FetchManager) FetchURL(ctx context.Context, url, checksum, dest string, update bool) (string, error) {
	// Warn about missing checksum - checksums are strongly encouraged for reproducible builds
	if checksum == "" {
		util.LogWarning("No checksum provided for %s - consider adding one for reproducible builds", url)
	}

	// Create cache directory for downloads
	downloadDir := filepath.Join(fm.cacheDir, "downloads")
	if err := os.MkdirAll(downloadDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create download directory: %w", err)
	}

	filename := urlBasename(url)
	downloadPath := fm.downloadPath(url)
	metadataPath := downloadPath + ".meta.json"

	if update && checksum == "" {
		for _, path := range []string{downloadPath, metadataPath} {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return "", err
			}
		}
	}

	// A cached archive is reused only if it still verifies.
	if _, err := os.Stat(downloadPath); err == nil {
		archiveValid := true

		// Verify checksum: prefer config checksum, fall back to metadata SHA256
		if checksum != "" {
			if err := fm.verifyChecksum(downloadPath, checksum); err != nil {
				util.LogProgress("Cached archive checksum mismatch, re-downloading")
				archiveValid = false
			}
		} else if err := fm.verifyAgainstMetadata(downloadPath, metadataPath); err != nil {
			util.LogProgress("Cached archive failed metadata verification: %v", err)
			archiveValid = false
		}

		// Verify archive integrity (can we open/read it?)
		if archiveValid {
			if err := fm.verifyArchiveIntegrity(downloadPath, filename); err != nil {
				util.LogProgress("Cached archive appears corrupted, re-downloading: %v", err)
				archiveValid = false
			}
		}

		if !archiveValid {
			os.Remove(downloadPath)
			os.Remove(metadataPath)
		}
	}

	// Download if not already cached (or was removed due to corruption/checksum mismatch)
	if _, err := os.Stat(downloadPath); os.IsNotExist(err) {
		util.LogProgress("Fetching URL: %s -> %s", url, dest)
		// Download to a temp file first, only move to cache on success
		tempPath := downloadPath + ".tmp"

		if err := fm.downloadFile(ctx, url, tempPath); err != nil {
			os.Remove(tempPath)
			return "", fmt.Errorf("download failed: %w", err)
		}

		// Verify checksum if provided
		if checksum != "" {
			if err := fm.verifyChecksum(tempPath, checksum); err != nil {
				os.Remove(tempPath)
				return "", fmt.Errorf("checksum verification failed: %w", err)
			}
		}

		// Verify the archive can be read (integrity check)
		// Pass the original filename so we know the correct archive format
		if err := fm.verifyArchiveIntegrity(tempPath, filename); err != nil {
			os.Remove(tempPath)
			return "", fmt.Errorf("archive integrity check failed: %w", err)
		}

		// All checks passed - move temp file to final location
		if err := os.Rename(tempPath, downloadPath); err != nil {
			os.Remove(tempPath)
			return "", fmt.Errorf("failed to move downloaded file to cache: %w", err)
		}

		// Save metadata
		if err := fm.saveDownloadMetadata(metadataPath, url, checksum, downloadPath); err != nil {
			return "", fmt.Errorf("failed to save download metadata: %w", err)
		}
	}

	archiveHash, err := fm.computeFileHash(downloadPath)
	if err != nil {
		return "", fmt.Errorf("failed to hash %s: %w", downloadPath, err)
	}
	marker := extractionMarker{URL: url, SHA256: archiveHash}
	if fm.hasExtraction(dest, marker) {
		util.LogInfo("Using the extraction of %s in %s", url, dest)
		return marker.SHA256, nil
	}

	// Drop the old marker first: if this run dies mid-extraction, dest stays unmarked.
	os.Remove(extractionMarkerPath(dest))
	if err := extractArchive(downloadPath, filename, dest); err != nil {
		return "", fmt.Errorf("extraction of %s failed: %w", url, err)
	}
	if err := writeExtractionMarker(dest, marker); err != nil {
		return "", err
	}

	util.LogProgress("Successfully fetched and extracted %s", url)
	return archiveHash, nil
}

// urlBasename returns the file name at the end of a URL, for archive format detection and
// readable cache names.
func urlBasename(url string) string {
	name := url
	if i := strings.IndexAny(name, "?#"); i >= 0 {
		name = name[:i]
	}
	name = name[strings.LastIndex(name, "/")+1:]
	safe := strings.Map(func(r rune) rune {
		if r == '.' || r == '-' || r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return r
		}
		return '_'
	}, name)
	if safe == "" || safe == "." || safe == ".." {
		return "download"
	}
	return safe
}

// downloadPath returns where the archive for url is cached: a hash of the whole URL, so that
// URLs sharing a basename don't share a file, then the basename.
func (fm *FetchManager) downloadPath(url string) string {
	hash := sha256.Sum256([]byte(url))
	return filepath.Join(fm.cacheDir, "downloads", hex.EncodeToString(hash[:8])+"-"+urlBasename(url))
}

// extractionMarker records what an extraction directory was extracted from.
type extractionMarker struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

// extractionMarkerPath is the marker file next to an extraction directory (not inside it, so the
// fetched tree stays as the archive made it).
func extractionMarkerPath(dest string) string {
	return dest + ".extracted.json"
}

// ExtractedURL returns the SHA-256 of the archive of url that dest holds, as recorded when it was
// extracted, or "" if dest doesn't hold an extraction of url. It downloads and checks nothing.
func (fm *FetchManager) ExtractedURL(url, dest string) string {
	data, err := os.ReadFile(extractionMarkerPath(dest))
	if err != nil {
		return ""
	}
	var recorded extractionMarker
	if json.Unmarshal(data, &recorded) != nil || recorded.URL != url {
		return ""
	}
	return recorded.SHA256
}

// hasExtraction reports whether dest holds a complete extraction of the archive in marker.
func (fm *FetchManager) hasExtraction(dest string, marker extractionMarker) bool {
	data, err := os.ReadFile(extractionMarkerPath(dest))
	if err != nil {
		return false
	}
	var recorded extractionMarker
	if json.Unmarshal(data, &recorded) != nil || recorded != marker {
		return false
	}
	info, err := os.Stat(dest)
	return err == nil && info.IsDir()
}

func writeExtractionMarker(dest string, marker extractionMarker) error {
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(extractionMarkerPath(dest), data, 0o644); err != nil {
		return fmt.Errorf("failed to record the extraction of %s: %w", dest, err)
	}
	return nil
}

// verifyArchiveIntegrity checks if an archive can be opened and read
// This helps detect corrupted or truncated downloads
// originalFilename is used to determine the archive format (archivePath may be a temp file)
func (fm *FetchManager) verifyArchiveIntegrity(archivePath, originalFilename string) error {
	ext := strings.ToLower(filepath.Ext(originalFilename))

	// Handle double extensions like .tar.gz
	if strings.HasSuffix(strings.ToLower(originalFilename), ".tar.gz") || strings.HasSuffix(strings.ToLower(originalFilename), ".tgz") {
		return fm.verifyTarGzIntegrity(archivePath)
	}

	switch ext {
	case ".zip":
		return fm.verifyZipIntegrity(archivePath)
	case ".tar":
		return fm.verifyTarIntegrity(archivePath)
	case ".gz":
		if strings.HasSuffix(strings.ToLower(originalFilename), ".tar.gz") {
			return fm.verifyTarGzIntegrity(archivePath)
		}
		return fmt.Errorf("unsupported archive format for integrity check: %s", ext)
	default:
		return fmt.Errorf("unsupported archive format for integrity check: %s", ext)
	}
}

// verifyZipIntegrity checks if a zip file can be read
func (fm *FetchManager) verifyZipIntegrity(archivePath string) error {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("failed to open zip: %w", err)
	}
	defer r.Close()

	// Check that we can iterate entries
	if len(r.File) == 0 {
		return fmt.Errorf("zip archive is empty")
	}

	return nil
}

// verifyTarGzIntegrity checks if a tar.gz file can be read
func (fm *FetchManager) verifyTarGzIntegrity(archivePath string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	gzr, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)

	// Try to read at least one entry
	_, err = tr.Next()
	if err == io.EOF {
		return fmt.Errorf("tar archive is empty")
	}
	if err != nil {
		return fmt.Errorf("failed to read tar entry: %w", err)
	}

	return nil
}

// verifyTarIntegrity checks if a tar file can be read
func (fm *FetchManager) verifyTarIntegrity(archivePath string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	tr := tar.NewReader(file)

	// Try to read at least one entry
	_, err = tr.Next()
	if err == io.EOF {
		return fmt.Errorf("tar archive is empty")
	}
	if err != nil {
		return fmt.Errorf("failed to read tar entry: %w", err)
	}

	return nil
}

// saveDownloadMetadata saves metadata about a successful download
func (fm *FetchManager) saveDownloadMetadata(metadataPath, url, checksum, downloadPath string) error {
	info, err := os.Stat(downloadPath)
	if err != nil {
		return err
	}

	// Compute SHA256 hash of the downloaded file
	sha256Hash, err := fm.computeFileHash(downloadPath)
	if err != nil {
		return fmt.Errorf("failed to compute file hash: %w", err)
	}

	metadata := DownloadMetadata{
		URL:          url,
		Checksum:     checksum,
		SHA256:       sha256Hash,
		Size:         info.Size(),
		DownloadedAt: time.Now(),
		Filename:     filepath.Base(downloadPath),
	}

	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(metadataPath, data, 0644)
}

// computeFileHash computes the SHA256 hash of a file
func (fm *FetchManager) computeFileHash(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

// verifyAgainstMetadata verifies a cached archive against its stored metadata
// This is used when no config checksum is provided but we have metadata from a previous download
func (fm *FetchManager) verifyAgainstMetadata(archivePath, metadataPath string) error {
	// Load metadata
	metadata, err := fm.loadDownloadMetadata(metadataPath)
	if err != nil {
		// No metadata file - can't verify, but this is okay for legacy cached files
		util.LogDebug("No metadata file for cached archive, skipping verification")
		return nil
	}

	// Verify file size first (quick check)
	info, err := os.Stat(archivePath)
	if err != nil {
		return fmt.Errorf("failed to stat archive: %w", err)
	}
	if info.Size() != metadata.Size {
		return fmt.Errorf("file size mismatch: expected %d, got %d", metadata.Size, info.Size())
	}

	// Verify SHA256 hash
	if metadata.SHA256 == "" {
		util.LogDebug("Metadata exists but no SHA256 stored, skipping hash verification")
		return nil
	}

	actualHash, err := fm.computeFileHash(archivePath)
	if err != nil {
		return fmt.Errorf("failed to compute hash: %w", err)
	}

	if actualHash != metadata.SHA256 {
		return fmt.Errorf("SHA256 mismatch: expected %s, got %s", metadata.SHA256, actualHash)
	}

	util.LogDebug("Archive verified against metadata SHA256")
	return nil
}

// loadDownloadMetadata loads metadata from a JSON file
func (fm *FetchManager) loadDownloadMetadata(metadataPath string) (*DownloadMetadata, error) {
	data, err := os.ReadFile(metadataPath)
	if err != nil {
		return nil, err
	}

	var metadata DownloadMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil, err
	}

	return &metadata, nil
}

// GetDownloadMetadata returns the metadata for a cached download by URL
// This is useful for lockfile generation to get the SHA256 hash
func (fm *FetchManager) GetDownloadMetadata(url string) (*DownloadMetadata, error) {
	return fm.loadDownloadMetadata(fm.downloadPath(url) + ".meta.json")
}

// downloadFile downloads a file from a URL with progress reporting. ctx stops it (an interrupt).
// TODO: a timeout for a server that stops answering (review phase 4).
func (fm *FetchManager) downloadFile(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP error: %s", resp.Status)
	}

	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()

	// Get content length for progress reporting
	contentLength := resp.ContentLength
	if contentLength > 0 {
		util.LogProgress("Downloading: %s (%.1f MB)", url, float64(contentLength)/(1024*1024))
	} else {
		util.LogProgress("Downloading: %s (unknown size)", url)
	}

	// Copy with progress tracking
	var written int64
	buf := make([]byte, 32*1024) // 32KB buffer
	lastPercent := -1

	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			nw, writeErr := out.Write(buf[:n])
			if writeErr != nil {
				return writeErr
			}
			written += int64(nw)

			// Report progress every 10%
			if contentLength > 0 {
				percent := int(float64(written) / float64(contentLength) * 100)
				if percent/10 > lastPercent/10 {
					util.LogProgress("  %d%% (%.1f / %.1f MB)", percent,
						float64(written)/(1024*1024),
						float64(contentLength)/(1024*1024))
					lastPercent = percent
				}
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}

	util.LogProgress("Download complete: %s", filepath.Base(dest))
	return nil
}

// verifyChecksum verifies a file's checksum
func (fm *FetchManager) verifyChecksum(filePath, expected string) error {
	// Parse expected checksum (format: "sha256:...")
	parts := strings.SplitN(expected, ":", 2)
	algorithm := "sha256"
	expectedHash := expected

	if len(parts) == 2 {
		algorithm = parts[0]
		expectedHash = parts[1]
	}

	if algorithm != "sha256" {
		return fmt.Errorf("unsupported checksum algorithm: %s", algorithm)
	}

	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}

	actualHash := hex.EncodeToString(hash.Sum(nil))
	if actualHash != expectedHash {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", expectedHash, actualHash)
	}

	return nil
}

// CalculateChecksum calculates the SHA256 checksum of a file
func (fm *FetchManager) CalculateChecksum(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}
