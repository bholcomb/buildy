package resource

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

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
	log.Printf("Fetching git: %s @ %s -> %s", url, ref, dest)

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

	log.Printf("Successfully cloned %s", url)
	return nil
}

// updateGitRepo updates an existing git repository
func (fm *FetchManager) updateGitRepo(dest, ref string) error {
	log.Printf("Updating git repo: %s", dest)

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
	log.Printf("Fetching URL: %s -> %s", url, dest)

	// Check if destination already exists
	if _, err := os.Stat(dest); err == nil {
		log.Printf("Destination already exists, skipping download: %s", dest)
		return nil
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

	// Download if not already cached
	if _, err := os.Stat(downloadPath); os.IsNotExist(err) {
		if err := fm.downloadFile(url, downloadPath); err != nil {
			return fmt.Errorf("download failed: %w", err)
		}
	}

	// Verify checksum if provided
	if checksum != "" {
		if err := fm.verifyChecksum(downloadPath, checksum); err != nil {
			os.Remove(downloadPath) // Remove corrupted download
			return fmt.Errorf("checksum verification failed: %w", err)
		}
	}

	// Create destination directory
	if err := os.MkdirAll(dest, 0755); err != nil {
		return fmt.Errorf("failed to create destination directory: %w", err)
	}

	// Extract archive
	if err := fm.extractArchive(downloadPath, dest); err != nil {
		return fmt.Errorf("extraction failed: %w", err)
	}

	log.Printf("Successfully fetched and extracted %s", url)
	return nil
}

// downloadFile downloads a file from a URL
func (fm *FetchManager) downloadFile(url, dest string) error {
	log.Printf("Downloading: %s", url)

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

	_, err = io.Copy(out, resp.Body)
	return err
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
