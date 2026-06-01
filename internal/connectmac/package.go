package connectmac

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var defaultPackageExcludes = []string{
	"xcuserdata",
	".svn",
	".git",
	".DS_Store",
}

func PackagePath(path string, excludes []string) (string, func() error, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", nil, fmt.Errorf("read local path %s: %w", path, err)
	}
	if !info.IsDir() {
		return path, func() error { return nil }, nil
	}
	zipPath, err := zipDirectory(path, os.TempDir(), EffectiveSyncExcludes(excludes))
	if err != nil {
		return "", nil, err
	}
	return zipPath, func() error { return os.Remove(zipPath) }, nil
}

func EffectiveSyncExcludes(excludes []string) []string {
	if len(excludes) == 0 {
		return append([]string(nil), defaultPackageExcludes...)
	}
	return append([]string(nil), excludes...)
}

func zipDirectory(sourceDir, targetDir string, excludes []string) (string, error) {
	sourceDir, err := filepath.Abs(sourceDir)
	if err != nil {
		return "", err
	}
	base := filepath.Base(sourceDir)
	zipPath := filepath.Join(targetDir, fmt.Sprintf("%s-%s.zip", base, time.Now().Format("20060102150405")))
	file, err := os.Create(zipPath)
	if err != nil {
		return "", fmt.Errorf("create zip %s: %w", zipPath, err)
	}
	defer file.Close()

	writer := zip.NewWriter(file)
	defer writer.Close()

	err = filepath.WalkDir(sourceDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if shouldExclude(rel, entry.Name(), excludes) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		return addFileToZip(writer, sourceDir, path, base)
	})
	if err != nil {
		_ = os.Remove(zipPath)
		return "", fmt.Errorf("zip directory %s: %w", sourceDir, err)
	}
	return zipPath, nil
}

func shouldExclude(rel, name string, excludes []string) bool {
	rel = filepath.ToSlash(rel)
	for _, pattern := range excludes {
		switch {
		case strings.Contains(pattern, "*"):
			matched, _ := filepath.Match(pattern, name)
			if matched {
				return true
			}
		case name == pattern:
			return true
		case rel == pattern || strings.HasPrefix(rel, pattern+"/"):
			return true
		}
	}
	return false
}

func addFileToZip(writer *zip.Writer, sourceDir, path, base string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(sourceDir, path)
	if err != nil {
		return err
	}
	header, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	header.Name = filepath.ToSlash(filepath.Join(base, rel))
	header.Method = zip.Deflate
	target, err := writer.CreateHeader(header)
	if err != nil {
		return err
	}
	source, err := os.Open(path)
	if err != nil {
		return err
	}
	defer source.Close()
	_, err = io.Copy(target, source)
	return err
}
