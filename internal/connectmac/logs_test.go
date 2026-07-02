package connectmac

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLogManagerWriteCleanAndExport(t *testing.T) {
	dir := t.TempDir()
	manager := NewLogManager(dir)
	manager.Now = func() time.Time {
		return time.Date(2026, 7, 2, 10, 30, 0, 0, time.UTC)
	}
	if err := manager.Write(LogEntry{
		Level:   "error",
		Action:  "web.aws.status",
		Profile: "iossupport-usw2",
		Message: "aws status failed with password=secret-token",
	}); err != nil {
		t.Fatalf("write log: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "cm-2026-07-02.log"))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(data), "iossupport-usw2") || strings.Contains(string(data), "password=secret-token") {
		t.Fatalf("unexpected log data: %s", data)
	}
	oldPath := filepath.Join(dir, "cm-2026-05-01.log")
	if err := os.WriteFile(oldPath, []byte("old\n"), 0o600); err != nil {
		t.Fatalf("write old log: %v", err)
	}
	oldTime := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatalf("chtimes old log: %v", err)
	}
	exportPath := filepath.Join(dir, "export.zip")
	got, err := manager.Export(exportPath, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("export logs: %v", err)
	}
	if got != exportPath {
		t.Fatalf("export path = %s, want %s", got, exportPath)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("old log should be cleaned, err=%v", err)
	}
	zr, err := zip.OpenReader(exportPath)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	defer zr.Close()
	if len(zr.File) != 1 || zr.File[0].Name != "cm-2026-07-02.log" {
		t.Fatalf("zip files = %+v", zr.File)
	}
}
