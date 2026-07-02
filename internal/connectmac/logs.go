package connectmac

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const DefaultLogDir = "~/.connectmac/logs"

type LogManager struct {
	Dir string
	Now func() time.Time
}

type LogEntry struct {
	Time       string `json:"time"`
	Level      string `json:"level"`
	Action     string `json:"action"`
	Profile    string `json:"profile,omitempty"`
	AppleEmail string `json:"apple_email,omitempty"`
	Region     string `json:"region,omitempty"`
	AWSProfile string `json:"aws_profile,omitempty"`
	Message    string `json:"message"`
}

type LogFile struct {
	Path    string
	Name    string
	ModTime time.Time
	Size    int64
}

func NewLogManager(dir string) LogManager {
	return LogManager{Dir: dir, Now: time.Now}
}

func (m LogManager) normalize() LogManager {
	if m.Dir == "" {
		m.Dir = DefaultLogDir
	}
	if m.Now == nil {
		m.Now = time.Now
	}
	return m
}

func (m LogManager) Write(entry LogEntry) error {
	m = m.normalize()
	if entry.Level == "" {
		entry.Level = "info"
	}
	if entry.Time == "" {
		entry.Time = m.Now().Format(time.RFC3339)
	}
	entry.Message = sanitizeLogText(entry.Message)
	dir, err := ExpandPath(m.Dir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := m.Clean(30 * 24 * time.Hour); err != nil {
		return err
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "cm-"+m.Now().Format("2006-01-02")+".log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

func (m LogManager) List() ([]LogFile, error) {
	m = m.normalize()
	dir, err := ExpandPath(m.Dir)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []LogFile{}, nil
		}
		return nil, err
	}
	files := []LogFile{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".log") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		files = append(files, LogFile{
			Path:    filepath.Join(dir, entry.Name()),
			Name:    entry.Name(),
			ModTime: info.ModTime(),
			Size:    info.Size(),
		})
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].Name < files[j].Name
	})
	return files, nil
}

func (m LogManager) Clean(retention time.Duration) error {
	if retention <= 0 {
		retention = 30 * 24 * time.Hour
	}
	files, err := m.List()
	if err != nil {
		return err
	}
	cutoff := m.normalize().Now().Add(-retention)
	for _, file := range files {
		if file.ModTime.Before(cutoff) {
			if err := os.Remove(file.Path); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}

func (m LogManager) Export(dest string, retention time.Duration) (string, error) {
	m = m.normalize()
	if retention <= 0 {
		retention = 30 * 24 * time.Hour
	}
	if err := m.Clean(retention); err != nil {
		return "", err
	}
	files, err := m.List()
	if err != nil {
		return "", err
	}
	cutoff := m.Now().Add(-retention)
	if dest == "" {
		dest = fmt.Sprintf("connectmac-logs-%s.zip", m.Now().Format("20060102-150405"))
	}
	dest, err = filepath.Abs(dest)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", err
	}
	out, err := os.Create(dest)
	if err != nil {
		return "", err
	}
	defer out.Close()
	zw := zip.NewWriter(out)
	defer zw.Close()
	for _, file := range files {
		if file.ModTime.Before(cutoff) {
			continue
		}
		if err := addLogFileToZip(zw, file); err != nil {
			return "", err
		}
	}
	return dest, nil
}

func addLogFileToZip(zw *zip.Writer, file LogFile) error {
	in, err := os.Open(file.Path)
	if err != nil {
		return err
	}
	defer in.Close()
	header := &zip.FileHeader{
		Name:   file.Name,
		Method: zip.Deflate,
	}
	header.SetModTime(file.ModTime)
	w, err := zw.CreateHeader(header)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, in)
	return err
}

func sanitizeLogText(text string) string {
	text = strings.TrimSpace(text)
	if len(text) > 4000 {
		text = text[:4000]
	}
	replacements := []string{
		"password", "[password]",
		"Password", "[password]",
		"secret", "[secret]",
		"Secret", "[secret]",
		"token", "[token]",
		"Token", "[token]",
		"session", "[session]",
		"Session", "[session]",
	}
	replacer := strings.NewReplacer(replacements...)
	return replacer.Replace(text)
}
