package resource

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
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

// FetchGit clones or updates a git repository
func (fm *FetchManager) FetchGit(url, ref, dest string) error {
	util.LogProgress("Fetching git: %s @ %s -> %s", url, ref, dest)

	// Check if destination already exists
	if _, err := os.Stat(dest); err == nil {
		// Directory exists, try to update
		return fm.updateGitRepo(dest, ref)
	}

	// Clone the repository
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return fmt.Errorf("failed to create parent directory: %w", err)
	}

	cloneArgs := []string{"clone", "--depth", "1"}
	if ref != "" && !strings.HasPrefix(ref, "v") && len(ref) != 40 {
		// It's a branch name, not a tag or commit hash
		cloneArgs = append(cloneArgs, "--branch", ref)
	}
	cloneArgs = append(cloneArgs, url, dest)

	cmd := exec.Command("git", cloneArgs...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git clone failed: %s", output)
	}

	// If ref is a tag or commit, checkout that ref
	if ref != "" {
		checkoutCmd := exec.Command("git", "checkout", ref)
		checkoutCmd.Dir = dest
		if _, err := checkoutCmd.CombinedOutput(); err != nil {
			// Try fetching if checkout fails (might be a tag not in shallow clone)
			fetchCmd := exec.Command("git", "fetch", "--tags", "--depth", "1", "origin", ref)
			fetchCmd.Dir = dest
			fetchCmd.Run()

			checkoutCmd = exec.Command("git", "checkout", ref)
			checkoutCmd.Dir = dest
			if output, err := checkoutCmd.CombinedOutput(); err != nil {
				return fmt.Errorf("git checkout failed: %s", output)
			}
		}
	}

	util.LogProgress("Successfully cloned %s", url)
	return nil
}

// updateGitRepo updates an existing git repository
func (fm *FetchManager) updateGitRepo(dest, ref string) error {
	util.LogProgress("Updating git repo: %s", dest)

	// Fetch latest
	fetchCmd := exec.Command("git", "fetch", "--tags", "origin")
	fetchCmd.Dir = dest
	if output, err := fetchCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git fetch failed: %s", output)
	}

	// Checkout the ref if specified
	if ref != "" {
		checkoutCmd := exec.Command("git", "checkout", ref)
		checkoutCmd.Dir = dest
		if output, err := checkoutCmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git checkout failed: %s", output)
		}
	}

	return nil
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

// FetchURL downloads and extracts an archive from a URL
func (fm *FetchManager) FetchURL(url, checksum, dest string) error {
	util.LogProgress("Fetching URL: %s -> %s", url, dest)

	// Warn about missing checksum - checksums are strongly encouraged for reproducible builds
	if checksum == "" {
		util.LogWarning("No checksum provided for %s - consider adding one for reproducible builds", url)
	}

	// Create cache directory for downloads
	downloadDir := filepath.Join(fm.cacheDir, "downloads")
	if err := os.MkdirAll(downloadDir, 0755); err != nil {
		return fmt.Errorf("failed to create download directory: %w", err)
	}

	// Generate filename from URL
	filename := filepath.Base(url)
	if filename == "" || filename == "." {
		// Use hash of URL as filename
		hash := sha256.Sum256([]byte(url))
		filename = hex.EncodeToString(hash[:8])
	}
	downloadPath := filepath.Join(downloadDir, filename)
	metadataPath := downloadPath + ".meta.json"

	// Check if we can skip: archive exists in cache, is valid, and dest has files
	canSkip := false
	if _, err := os.Stat(downloadPath); err == nil {
		archiveValid := true

		// Verify checksum: prefer config checksum, fall back to metadata SHA256
		if checksum != "" {
			if err := fm.verifyChecksum(downloadPath, checksum); err != nil {
				util.LogProgress("Cached archive checksum mismatch, re-downloading")
				os.Remove(downloadPath)
				os.Remove(metadataPath)
				archiveValid = false
			}
		} else {
			// No config checksum - try to verify against stored metadata
			if err := fm.verifyAgainstMetadata(downloadPath, metadataPath); err != nil {
				util.LogProgress("Cached archive failed metadata verification: %v", err)
				os.Remove(downloadPath)
				os.Remove(metadataPath)
				archiveValid = false
			}
		}

		// Verify archive integrity (can we open/read it?)
		if archiveValid {
			if err := fm.verifyArchiveIntegrity(downloadPath, filename); err != nil {
				util.LogProgress("Cached archive appears corrupted, re-downloading: %v", err)
				os.Remove(downloadPath)
				os.Remove(metadataPath)
				archiveValid = false
			}
		}

		if archiveValid {
			// Check if destination exists and has content
			if fm.isValidExtraction(dest) {
				util.LogProgress("Using cached archive, destination exists: %s", dest)
				canSkip = true
			}
			// If archive is valid but dest doesn't exist, we fall through to extraction
		}
	}

	if canSkip {
		return nil
	}

	// Clean up any partial extraction before re-extracting
	if _, err := os.Stat(dest); err == nil {
		util.LogProgress("Cleaning up incomplete extraction: %s", dest)
		os.RemoveAll(dest)
	}

	// Download if not already cached (or was removed due to corruption/checksum mismatch)
	if _, err := os.Stat(downloadPath); os.IsNotExist(err) {
		// Download to a temp file first, only move to cache on success
		tempPath := downloadPath + ".tmp"

		if err := fm.downloadFile(url, tempPath); err != nil {
			os.Remove(tempPath)
			return fmt.Errorf("download failed: %w", err)
		}

		// Verify checksum if provided
		if checksum != "" {
			if err := fm.verifyChecksum(tempPath, checksum); err != nil {
				os.Remove(tempPath)
				return fmt.Errorf("checksum verification failed: %w", err)
			}
		}

		// Verify the archive can be read (integrity check)
		// Pass the original filename so we know the correct archive format
		if err := fm.verifyArchiveIntegrity(tempPath, filename); err != nil {
			os.Remove(tempPath)
			return fmt.Errorf("archive integrity check failed: %w", err)
		}

		// All checks passed - move temp file to final location
		if err := os.Rename(tempPath, downloadPath); err != nil {
			os.Remove(tempPath)
			return fmt.Errorf("failed to move downloaded file to cache: %w", err)
		}

		// Save metadata
		if err := fm.saveDownloadMetadata(metadataPath, url, checksum, downloadPath); err != nil {
			util.LogWarning("Failed to save download metadata: %v", err)
		}
	}

	// Create destination directory
	if err := os.MkdirAll(dest, 0755); err != nil {
		return fmt.Errorf("failed to create destination directory: %w", err)
	}

	// Extract archive
	if err := fm.extractArchive(downloadPath, dest); err != nil {
		os.RemoveAll(dest)
		return fmt.Errorf("extraction failed: %w", err)
	}

	util.LogProgress("Successfully fetched and extracted %s", url)
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
	downloadDir := filepath.Join(fm.cacheDir, "downloads")

	// Generate filename from URL (same logic as FetchURL)
	filename := filepath.Base(url)
	if filename == "" || filename == "." {
		hash := sha256.Sum256([]byte(url))
		filename = hex.EncodeToString(hash[:8])
	}
	metadataPath := filepath.Join(downloadDir, filename+".meta.json")

	return fm.loadDownloadMetadata(metadataPath)
}

// isValidExtraction checks if a destination directory exists and contains files
// This helps detect incomplete extractions without using marker files
func (fm *FetchManager) isValidExtraction(dest string) bool {
	info, err := os.Stat(dest)
	if err != nil || !info.IsDir() {
		return false
	}

	// Check that directory is not empty - read first few entries
	entries, err := os.ReadDir(dest)
	if err != nil || len(entries) == 0 {
		return false
	}

	// For extra validation, check that at least one entry is a directory or file
	// (not just a symlink or empty structure)
	for _, entry := range entries {
		entryPath := filepath.Join(dest, entry.Name())
		if info, err := os.Stat(entryPath); err == nil {
			if info.IsDir() || info.Size() > 0 {
				return true
			}
		}
	}

	return false
}

// downloadFile downloads a file from a URL with progress reporting
func (fm *FetchManager) downloadFile(url, dest string) error {
	resp, err := http.Get(url)
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

// extractArchive extracts an archive to a destination directory
func (fm *FetchManager) extractArchive(archivePath, dest string) error {
	ext := strings.ToLower(filepath.Ext(archivePath))

	// Handle double extensions like .tar.gz
	if strings.HasSuffix(strings.ToLower(archivePath), ".tar.gz") || strings.HasSuffix(strings.ToLower(archivePath), ".tgz") {
		return fm.extractTarGz(archivePath, dest)
	}

	switch ext {
	case ".zip":
		return fm.extractZip(archivePath, dest)
	case ".tar":
		return fm.extractTar(archivePath, dest)
	case ".gz":
		// Check if it's a tar.gz
		if strings.HasSuffix(strings.ToLower(archivePath), ".tar.gz") {
			return fm.extractTarGz(archivePath, dest)
		}
		return fmt.Errorf("unsupported archive format: %s", ext)
	default:
		return fmt.Errorf("unsupported archive format: %s", ext)
	}
}

// extractTarGz extracts a .tar.gz archive
func (fm *FetchManager) extractTarGz(archivePath, dest string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()

	gzr, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gzr.Close()

	return fm.extractTarReader(tar.NewReader(gzr), dest)
}

// extractTar extracts a .tar archive
func (fm *FetchManager) extractTar(archivePath, dest string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()

	return fm.extractTarReader(tar.NewReader(file), dest)
}

// extractTarReader extracts from a tar reader
func (fm *FetchManager) extractTarReader(tr *tar.Reader, dest string) error {
	// Track the common prefix to strip (e.g., "project-1.0.0/")
	var commonPrefix string
	firstEntry := true

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		// Determine common prefix from first entry
		if firstEntry {
			parts := strings.SplitN(header.Name, "/", 2)
			if len(parts) > 1 {
				commonPrefix = parts[0] + "/"
			}
			firstEntry = false
		}

		// Strip common prefix
		name := header.Name
		if commonPrefix != "" && strings.HasPrefix(name, commonPrefix) {
			name = strings.TrimPrefix(name, commonPrefix)
		}

		if name == "" {
			continue
		}

		target := filepath.Join(dest, name)

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(header.Mode)); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			outFile, err := os.Create(target)
			if err != nil {
				return err
			}
			if _, err := io.Copy(outFile, tr); err != nil {
				outFile.Close()
				return err
			}
			outFile.Close()
			os.Chmod(target, os.FileMode(header.Mode))
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			os.Symlink(header.Linkname, target)
		}
	}

	return nil
}

// extractZip extracts a .zip archive
func (fm *FetchManager) extractZip(archivePath, dest string) error {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer r.Close()

	// Track the common prefix to strip
	var commonPrefix string
	if len(r.File) > 0 {
		parts := strings.SplitN(r.File[0].Name, "/", 2)
		if len(parts) > 1 {
			commonPrefix = parts[0] + "/"
		}
	}

	for _, f := range r.File {
		// Strip common prefix
		name := f.Name
		if commonPrefix != "" && strings.HasPrefix(name, commonPrefix) {
			name = strings.TrimPrefix(name, commonPrefix)
		}

		if name == "" {
			continue
		}

		target := filepath.Join(dest, name)

		if f.FileInfo().IsDir() {
			os.MkdirAll(target, f.Mode())
			continue
		}

		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}

		outFile, err := os.Create(target)
		if err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			outFile.Close()
			return err
		}

		_, err = io.Copy(outFile, rc)
		rc.Close()
		outFile.Close()

		if err != nil {
			return err
		}

		os.Chmod(target, f.Mode())
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
