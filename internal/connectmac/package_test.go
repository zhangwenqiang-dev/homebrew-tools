package connectmac

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestZipDirectoryExcludesIgnoredPaths(t *testing.T) {
	source := t.TempDir()
	writeFile(t, filepath.Join(source, "keep.txt"), "keep")
	writeFile(t, filepath.Join(source, ".git", "config"), "git")
	writeFile(t, filepath.Join(source, "App.xcodeproj", "xcuserdata", "user.xcuserdatad"), "user")

	zipPath, err := zipDirectory(source, t.TempDir(), defaultPackageExcludes)
	if err != nil {
		t.Fatalf("zipDirectory returned error: %v", err)
	}
	names := zipNames(t, zipPath)
	if !contains(names, filepath.Base(source)+"/keep.txt") {
		t.Fatalf("expected keep.txt in zip, got %v", names)
	}
	for _, excluded := range []string{".git/config", "xcuserdata"} {
		if containsSubstring(names, excluded) {
			t.Fatalf("expected %s to be excluded, got %v", excluded, names)
		}
	}
}

func TestZipDirectoryUsesConfiguredExcludes(t *testing.T) {
	source := t.TempDir()
	writeFile(t, filepath.Join(source, "keep.txt"), "keep")
	writeFile(t, filepath.Join(source, "docs", "readme.txt"), "docs")
	writeFile(t, filepath.Join(source, "notes.md"), "markdown")
	zipPath, err := zipDirectory(source, t.TempDir(), []string{"docs", "*.md"})
	if err != nil {
		t.Fatalf("zipDirectory returned error: %v", err)
	}
	names := zipNames(t, zipPath)
	if containsSubstring(names, "docs/readme.txt") || containsSubstring(names, "notes.md") {
		t.Fatalf("configured excludes were not applied: %v", names)
	}
}

func TestPackagePathReturnsFileAsIs(t *testing.T) {
	file := filepath.Join(t.TempDir(), "a.txt")
	writeFile(t, file, "a")
	got, cleanup, err := PackagePath(file, nil)
	if err != nil {
		t.Fatalf("PackagePath returned error: %v", err)
	}
	if got != file {
		t.Fatalf("got %q, want %q", got, file)
	}
	if err := cleanup(); err != nil {
		t.Fatalf("cleanup returned error: %v", err)
	}
}

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func zipNames(t *testing.T, path string) []string {
	t.Helper()
	reader, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	names := make([]string, 0, len(reader.File))
	for _, file := range reader.File {
		names = append(names, file.Name)
	}
	return names
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func containsSubstring(items []string, want string) bool {
	for _, item := range items {
		if strings.Contains(filepath.ToSlash(item), want) {
			return true
		}
	}
	return false
}
