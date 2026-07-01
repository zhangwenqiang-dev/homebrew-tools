package connectmac

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const DefaultMemberDataPath = "~/.connectmac/members.json"

type MemberStore struct {
	Path string
	Now  func() time.Time
}

type MemberData struct {
	Members     []Member             `json:"members"`
	Assignments []AppleAccountMember `json:"assignments"`
	Events      []OperationEvent     `json:"events"`
}

type Member struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	Role      string `json:"role"`
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type AppleAccountMember struct {
	AppleEmail string `json:"apple_email"`
	MemberID   string `json:"member_id"`
	Relation   string `json:"relation"`
	CreatedAt  string `json:"created_at"`
}

type OperationEvent struct {
	ID         string `json:"id"`
	Action     string `json:"action"`
	Profile    string `json:"profile"`
	AppleEmail string `json:"apple_email,omitempty"`
	MemberID   string `json:"member_id,omitempty"`
	Confirmed  bool   `json:"confirmed"`
	Status     string `json:"status"`
	Message    string `json:"message,omitempty"`
	CreatedAt  string `json:"created_at"`
}

type MemberWithAssignments struct {
	Member
	Assignments []AppleAccountMember `json:"assignments"`
}

func NewMemberStore(path string) MemberStore {
	return MemberStore{Path: path, Now: time.Now}
}

func (s MemberStore) normalize() MemberStore {
	if s.Path == "" {
		s.Path = DefaultMemberDataPath
	}
	if s.Now == nil {
		s.Now = time.Now
	}
	return s
}

func (s MemberStore) Load() (MemberData, error) {
	s = s.normalize()
	path, err := ExpandPath(s.Path)
	if err != nil {
		return MemberData{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return MemberData{Members: []Member{}, Assignments: []AppleAccountMember{}, Events: []OperationEvent{}}, nil
		}
		return MemberData{}, err
	}
	var db MemberData
	if err := json.Unmarshal(data, &db); err != nil {
		return MemberData{}, fmt.Errorf("parse members data %s: %w", path, err)
	}
	normalizeMemberData(&db)
	return db, nil
}

func (s MemberStore) Save(db MemberData) error {
	s = s.normalize()
	normalizeMemberData(&db)
	path, err := ExpandPath(s.Path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
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

func (s MemberStore) ListMembers() ([]MemberWithAssignments, error) {
	db, err := s.Load()
	if err != nil {
		return nil, err
	}
	out := make([]MemberWithAssignments, 0, len(db.Members))
	for _, member := range db.Members {
		item := MemberWithAssignments{Member: member}
		for _, assignment := range db.Assignments {
			if assignment.MemberID == member.ID {
				item.Assignments = append(item.Assignments, assignment)
			}
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Email) < strings.ToLower(out[j].Email)
	})
	return out, nil
}

func (s MemberStore) AddMember(name, email, role string) (Member, error) {
	name = strings.TrimSpace(name)
	email = normalizeEmail(email)
	if strings.TrimSpace(role) == "" {
		role = "operator"
	}
	role = normalizeMemberRole(role)
	if name == "" {
		return Member{}, errors.New("name is required")
	}
	if email == "" || !strings.Contains(email, "@") {
		return Member{}, errors.New("valid email is required")
	}
	if role == "" {
		return Member{}, errors.New("role must be admin, operator, or viewer")
	}
	db, err := s.Load()
	if err != nil {
		return Member{}, err
	}
	if _, ok := findMemberByEmail(db, email); ok {
		return Member{}, fmt.Errorf("member %s already exists", email)
	}
	now := s.normalize().Now().Format(time.RFC3339)
	member := Member{
		ID:        "member-" + slugPart(email),
		Name:      name,
		Email:     email,
		Role:      role,
		Enabled:   true,
		CreatedAt: now,
		UpdatedAt: now,
	}
	db.Members = append(db.Members, member)
	return member, s.Save(db)
}

func (s MemberStore) SetMemberEnabled(email string, enabled bool) (Member, error) {
	email = normalizeEmail(email)
	db, err := s.Load()
	if err != nil {
		return Member{}, err
	}
	idx, ok := findMemberIndexByEmail(db, email)
	if !ok {
		return Member{}, fmt.Errorf("member %s not found", email)
	}
	db.Members[idx].Enabled = enabled
	db.Members[idx].UpdatedAt = s.normalize().Now().Format(time.RFC3339)
	return db.Members[idx], s.Save(db)
}

func (s MemberStore) AssignMember(appleEmail, memberEmail, relation string) (AppleAccountMember, error) {
	appleEmail = normalizeEmail(appleEmail)
	memberEmail = normalizeEmail(memberEmail)
	relation = strings.TrimSpace(strings.ToLower(relation))
	if appleEmail == "" || !strings.Contains(appleEmail, "@") {
		return AppleAccountMember{}, errors.New("valid apple email is required")
	}
	if relation == "" {
		relation = "owner"
	}
	db, err := s.Load()
	if err != nil {
		return AppleAccountMember{}, err
	}
	member, ok := findMemberByEmail(db, memberEmail)
	if !ok {
		return AppleAccountMember{}, fmt.Errorf("member %s not found", memberEmail)
	}
	for _, assignment := range db.Assignments {
		if strings.EqualFold(assignment.AppleEmail, appleEmail) && assignment.MemberID == member.ID {
			return assignment, nil
		}
	}
	assignment := AppleAccountMember{
		AppleEmail: appleEmail,
		MemberID:   member.ID,
		Relation:   relation,
		CreatedAt:  s.normalize().Now().Format(time.RFC3339),
	}
	db.Assignments = append(db.Assignments, assignment)
	return assignment, s.Save(db)
}

func (s MemberStore) UnassignMember(appleEmail, memberEmail string) error {
	appleEmail = normalizeEmail(appleEmail)
	memberEmail = normalizeEmail(memberEmail)
	db, err := s.Load()
	if err != nil {
		return err
	}
	member, ok := findMemberByEmail(db, memberEmail)
	if !ok {
		return fmt.Errorf("member %s not found", memberEmail)
	}
	out := db.Assignments[:0]
	removed := false
	for _, assignment := range db.Assignments {
		if strings.EqualFold(assignment.AppleEmail, appleEmail) && assignment.MemberID == member.ID {
			removed = true
			continue
		}
		out = append(out, assignment)
	}
	if !removed {
		return fmt.Errorf("assignment not found")
	}
	db.Assignments = out
	return s.Save(db)
}

func (s MemberStore) MembersForApple(appleEmail string) ([]Member, error) {
	appleEmail = normalizeEmail(appleEmail)
	db, err := s.Load()
	if err != nil {
		return nil, err
	}
	var members []Member
	for _, assignment := range db.Assignments {
		if !strings.EqualFold(assignment.AppleEmail, appleEmail) {
			continue
		}
		if member, ok := findMemberByID(db, assignment.MemberID); ok {
			members = append(members, member)
		}
	}
	return members, nil
}

func (s MemberStore) RecordEvent(event OperationEvent) error {
	db, err := s.Load()
	if err != nil {
		return err
	}
	now := s.normalize().Now().Format(time.RFC3339)
	if event.ID == "" {
		event.ID = "event-" + strings.ReplaceAll(now, ":", "") + "-" + slugPart(event.Action+"-"+event.Profile)
	}
	if event.CreatedAt == "" {
		event.CreatedAt = now
	}
	db.Events = append(db.Events, event)
	if len(db.Events) > 500 {
		db.Events = db.Events[len(db.Events)-500:]
	}
	return s.Save(db)
}

func (s MemberStore) RecentEvents(appleEmail string, limit int) ([]OperationEvent, error) {
	db, err := s.Load()
	if err != nil {
		return nil, err
	}
	appleEmail = normalizeEmail(appleEmail)
	var out []OperationEvent
	for i := len(db.Events) - 1; i >= 0; i-- {
		event := db.Events[i]
		if appleEmail != "" && !strings.EqualFold(event.AppleEmail, appleEmail) {
			continue
		}
		out = append(out, event)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func normalizeMemberData(db *MemberData) {
	if db.Members == nil {
		db.Members = []Member{}
	}
	if db.Assignments == nil {
		db.Assignments = []AppleAccountMember{}
	}
	if db.Events == nil {
		db.Events = []OperationEvent{}
	}
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func normalizeMemberRole(role string) string {
	role = strings.TrimSpace(strings.ToLower(role))
	switch role {
	case "admin", "operator", "viewer":
		return role
	default:
		return ""
	}
}

func findMemberByEmail(db MemberData, email string) (Member, bool) {
	idx, ok := findMemberIndexByEmail(db, email)
	if !ok {
		return Member{}, false
	}
	return db.Members[idx], true
}

func findMemberIndexByEmail(db MemberData, email string) (int, bool) {
	email = normalizeEmail(email)
	for i, member := range db.Members {
		if strings.EqualFold(member.Email, email) {
			return i, true
		}
	}
	return 0, false
}

func findMemberByID(db MemberData, id string) (Member, bool) {
	for _, member := range db.Members {
		if member.ID == id {
			return member, true
		}
	}
	return Member{}, false
}
