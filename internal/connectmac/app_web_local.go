package connectmac

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type webLocalDirEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

func (a App) webLocalListHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeWebError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		path := strings.TrimSpace(r.URL.Query().Get("path"))
		roots := localPickerRoots()
		if path == "" {
			writeWebJSON(w, webAPIResponse{OK: true, Data: map[string]interface{}{
				"path":    "",
				"parent":  "",
				"entries": roots,
				"roots":   roots,
			}})
			return
		}
		cleanPath, err := filepath.Abs(filepath.Clean(path))
		if err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		root, ok := localPickerRootFor(cleanPath, roots)
		if !ok || hasHiddenRelativePath(root, cleanPath) {
			writeWebError(w, http.StatusBadRequest, "path is outside allowed local directories")
			return
		}
		entries, err := localPickerEntries(cleanPath)
		if err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		parent := ""
		if cleanPath != root {
			parent = filepath.Dir(cleanPath)
		}
		writeWebJSON(w, webAPIResponse{OK: true, Data: map[string]interface{}{
			"path":    cleanPath,
			"parent":  parent,
			"entries": entries,
			"roots":   roots,
		}})
	}
}

func localPickerRoots() []webLocalDirEntry {
	seen := map[string]bool{}
	var roots []webLocalDirEntry
	add := func(label, path string) {
		if strings.TrimSpace(path) == "" {
			return
		}
		clean, err := filepath.Abs(filepath.Clean(path))
		if err != nil || seen[clean] {
			return
		}
		info, err := os.Stat(clean)
		if err != nil || !info.IsDir() {
			return
		}
		seen[clean] = true
		roots = append(roots, webLocalDirEntry{Name: label, Path: clean})
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		add("Desktop", filepath.Join(home, "Desktop"))
		add("Documents", filepath.Join(home, "Documents"))
		add("Downloads", filepath.Join(home, "Downloads"))
		add("Pictures", filepath.Join(home, "Pictures"))
		add("Movies", filepath.Join(home, "Movies"))
		add("Music", filepath.Join(home, "Music"))
	}
	if cwd, err := os.Getwd(); err == nil {
		add("cm web current directory", cwd)
	}
	return roots
}

func localPickerRootFor(path string, roots []webLocalDirEntry) (string, bool) {
	for _, root := range roots {
		rel, err := filepath.Rel(root.Path, path)
		if err != nil {
			continue
		}
		if rel == "." || (!strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && rel != "..") {
			return root.Path, true
		}
	}
	return "", false
}

func hasHiddenRelativePath(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return false
	}
	for _, part := range strings.Split(rel, string(os.PathSeparator)) {
		if strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
}

func localPickerEntries(path string) ([]webLocalDirEntry, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	out := make([]webLocalDirEntry, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") || !entry.IsDir() {
			continue
		}
		out = append(out, webLocalDirEntry{Name: name, Path: filepath.Join(path, name)})
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}
