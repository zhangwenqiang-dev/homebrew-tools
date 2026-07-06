package connectmac

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const DefaultSyncHistoryPath = "~/.connectmac/sync-history.json"

type SyncHistoryStore struct {
	Path string
	Now  func() time.Time
}

type SyncHistoryData struct {
	Items []SyncHistoryItem `json:"items"`
}

type SyncHistoryItem struct {
	ID         string `json:"id"`
	Profile    string `json:"profile"`
	AppleEmail string `json:"apple_email,omitempty"`
	Direction  string `json:"direction"`
	LocalPath  string `json:"local_path,omitempty"`
	RemotePath string `json:"remote_path,omitempty"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

func NewSyncHistoryStore(path string) SyncHistoryStore {
	return SyncHistoryStore{Path: path, Now: time.Now}
}

func (s SyncHistoryStore) normalize() SyncHistoryStore {
	if s.Path == "" {
		s.Path = DefaultSyncHistoryPath
	}
	if s.Now == nil {
		s.Now = time.Now
	}
	return s
}

func (s SyncHistoryStore) Load() (SyncHistoryData, error) {
	s = s.normalize()
	path, err := ExpandPath(s.Path)
	if err != nil {
		return SyncHistoryData{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return SyncHistoryData{Items: []SyncHistoryItem{}}, nil
		}
		return SyncHistoryData{}, err
	}
	var db SyncHistoryData
	if err := json.Unmarshal(data, &db); err != nil {
		return SyncHistoryData{}, err
	}
	if db.Items == nil {
		db.Items = []SyncHistoryItem{}
	}
	return db, nil
}

func (s SyncHistoryStore) Save(db SyncHistoryData) error {
	s = s.normalize()
	path, err := ExpandPath(s.Path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if db.Items == nil {
		db.Items = []SyncHistoryItem{}
	}
	data, err := json.MarshalIndent(db, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s SyncHistoryStore) List(profile string, limit int) ([]SyncHistoryItem, error) {
	db, err := s.Load()
	if err != nil {
		return nil, err
	}
	profile = strings.TrimSpace(profile)
	items := make([]SyncHistoryItem, 0, len(db.Items))
	for _, item := range db.Items {
		if profile == "" || item.Profile == profile {
			items = append(items, item)
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].UpdatedAt > items[j].UpdatedAt
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (s SyncHistoryStore) Upsert(item SyncHistoryItem) (SyncHistoryItem, error) {
	s = s.normalize()
	now := s.Now().Format(time.RFC3339)
	item.Profile = strings.TrimSpace(item.Profile)
	item.AppleEmail = strings.TrimSpace(item.AppleEmail)
	item.Direction = strings.TrimSpace(item.Direction)
	item.LocalPath = strings.TrimSpace(item.LocalPath)
	item.RemotePath = strings.TrimSpace(item.RemotePath)
	db, err := s.Load()
	if err != nil {
		return SyncHistoryItem{}, err
	}
	for i := range db.Items {
		current := db.Items[i]
		if current.Profile == item.Profile && current.Direction == item.Direction && current.LocalPath == item.LocalPath && current.RemotePath == item.RemotePath {
			db.Items[i].AppleEmail = item.AppleEmail
			db.Items[i].UpdatedAt = now
			if db.Items[i].CreatedAt == "" {
				db.Items[i].CreatedAt = now
			}
			if err := s.Save(trimSyncHistory(db)); err != nil {
				return SyncHistoryItem{}, err
			}
			return db.Items[i], nil
		}
	}
	if item.ID == "" {
		item.ID = "sync-" + s.Now().Format("20060102150405.000000000")
		item.ID = strings.ReplaceAll(item.ID, ".", "")
	}
	item.CreatedAt = now
	item.UpdatedAt = now
	db.Items = append([]SyncHistoryItem{item}, db.Items...)
	if err := s.Save(trimSyncHistory(db)); err != nil {
		return SyncHistoryItem{}, err
	}
	return item, nil
}

func (s SyncHistoryStore) Delete(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("id is required")
	}
	db, err := s.Load()
	if err != nil {
		return err
	}
	next := db.Items[:0]
	for _, item := range db.Items {
		if item.ID != id {
			next = append(next, item)
		}
	}
	db.Items = next
	return s.Save(db)
}

func trimSyncHistory(db SyncHistoryData) SyncHistoryData {
	sort.SliceStable(db.Items, func(i, j int) bool {
		return db.Items[i].UpdatedAt > db.Items[j].UpdatedAt
	})
	if len(db.Items) > 100 {
		db.Items = db.Items[:100]
	}
	return db
}
