package connectmac

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
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

type MemberRepository interface {
	Load() (MemberData, error)
	Save(MemberData) error
	ListMembers() ([]MemberWithAssignments, error)
	AddMember(name, email, role string) (Member, error)
	AddMemberWithPassword(name, email, role, password string) (Member, error)
	SetupAdmin(name, email, password string) (Member, error)
	SetMemberPassword(emailOrUsername, password string) error
	UpdateMember(emailOrUsername, name, email, role string) (Member, error)
	UpdateMemberEmail(memberID, newEmail string) (Member, error)
	VerifyMemberPassword(emailOrUsername, password string) (Member, bool, error)
	HasPasswordMembers() (bool, error)
	SetMemberEnabled(email string, enabled bool) (Member, error)
	AssignMember(appleEmail, memberEmail, relation string) (AppleAccountMember, error)
	UnassignMember(appleEmail, memberEmail string) error
	MembersForApple(appleEmail string) ([]PublicMember, error)
	ProfileOwners() ([]PublicProfileOwner, error)
	ProfileOwner(profileName string) (PublicProfileOwner, bool, error)
	SetProfileOwner(profileName, memberEmail string) (PublicProfileOwner, error)
	ClearProfileOwner(profileName string) error
	ListManagedProfiles(memberEmail string) ([]ManagedProfile, error)
	UpsertManagedProfile(profile Profile) (ManagedProfile, error)
	SetManagedProfileEnabled(profileName string, enabled bool) (ManagedProfile, error)
	DeleteManagedProfile(profileName string) error
	AssignProfileAccess(profileName, memberEmail string) (ProfileAccess, error)
	UnassignProfileAccess(profileName, memberEmail string) error
	SetMemberProfileAccess(memberEmail string, profileNames []string) ([]ProfileAccess, error)
	MembersForProfile(profileName string) ([]PublicMember, error)
	ListReleaseReminders(memberEmail string) ([]ReleaseReminder, error)
	ReleaseReminder(profileName string) (ReleaseReminder, bool, error)
	UpsertReleaseReminder(reminder ReleaseReminder) (ReleaseReminder, error)
	UpdateReleaseReminder(profileName string, update func(ReleaseReminder) (ReleaseReminder, error)) (ReleaseReminder, error)
	MarkReleaseReminderDue(profileName, notifiedAt string) (ReleaseReminder, error)
	MarkReleaseReminderReleased(profileName, releasedAt string) (ReleaseReminder, error)
	WebSettings() (WebSettings, error)
	UpdateWebSettings(update WebSettings) (WebSettings, error)
	EnsureAuthSecret() (string, error)
	RecordEvent(event OperationEvent) error
	RecentEvents(appleEmail string, limit int) ([]OperationEvent, error)
}

type MemberData struct {
	Members       []Member             `json:"members"`
	Assignments   []AppleAccountMember `json:"assignments"`
	ProfileOwners []ProfileOwner       `json:"profile_owners"`
	Profiles      []ManagedProfile     `json:"profiles"`
	ProfileAccess []ProfileAccess      `json:"profile_access"`
	Reminders     []ReleaseReminder    `json:"release_reminders"`
	Events        []OperationEvent     `json:"events"`
	Settings      WebSettings          `json:"settings"`
}

type Member struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Email        string `json:"email"`
	Username     string `json:"username,omitempty"`
	Role         string `json:"role"`
	Enabled      bool   `json:"enabled"`
	PasswordHash string `json:"password_hash,omitempty"`
	PasswordSalt string `json:"password_salt,omitempty"`
	APITokenHash string `json:"api_token_hash,omitempty"`
	APITokenAt   string `json:"api_token_at,omitempty"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

type AppleAccountMember struct {
	AppleEmail string `json:"apple_email"`
	MemberID   string `json:"member_id"`
	Relation   string `json:"relation"`
	CreatedAt  string `json:"created_at"`
}

type ProfileOwner struct {
	ProfileName string `json:"profile_name"`
	MemberID    string `json:"member_id"`
	UpdatedAt   string `json:"updated_at"`
}

type PublicProfileOwner struct {
	ProfileName string       `json:"profile_name"`
	Owner       PublicMember `json:"owner"`
	UpdatedAt   string       `json:"updated_at"`
}

type ManagedProfile struct {
	Name        string `json:"name"`
	AppleEmail  string `json:"apple_email,omitempty"`
	Enabled     bool   `json:"enabled"`
	ProfileYAML string `json:"profile_yaml"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type ProfileAccess struct {
	ProfileName string `json:"profile_name"`
	MemberID    string `json:"member_id"`
	CreatedAt   string `json:"created_at"`
}

const (
	ReleaseReminderStatusActive      = "active"
	ReleaseReminderStatusDueNotified = "due_notified"
	ReleaseReminderStatusReleased    = "released"

	ReleaseReminderAutoReleaseStateScheduled = "scheduled"
	ReleaseReminderAutoReleaseStateRunning   = "running"
	ReleaseReminderAutoReleaseStateRetrying  = "retrying"
	ReleaseReminderAutoReleaseStateFailed    = "failed"
	ReleaseReminderAutoReleaseStateReleased  = "released"
)

type ReleaseReminder struct {
	ProfileName              string `json:"profile_name"`
	AppleEmail               string `json:"apple_email,omitempty"`
	HostID                   string `json:"host_id,omitempty"`
	HostCreatedAt            string `json:"host_created_at,omitempty"`
	ReleaseDueAt             string `json:"release_due_at,omitempty"`
	OwnerEmail               string `json:"owner_email,omitempty"`
	OwnerName                string `json:"owner_name,omitempty"`
	LastExtendedByEmail      string `json:"last_extended_by_email,omitempty"`
	LastExtendedByName       string `json:"last_extended_by_name,omitempty"`
	LastExtendedAt           string `json:"last_extended_at,omitempty"`
	LastNotifiedAt           string `json:"last_notified_at,omitempty"`
	ReleasedAt               string `json:"released_at,omitempty"`
	Status                   string `json:"status"`
	AutoReleaseEnabled       bool   `json:"auto_release_enabled"`
	AutoReleaseAt            string `json:"auto_release_at,omitempty"`
	AutoReleaseStartedAt     string `json:"auto_release_started_at,omitempty"`
	AutoReleaseLastAttemptAt string `json:"auto_release_last_attempt_at,omitempty"`
	AutoReleaseAttempts      int    `json:"auto_release_attempts,omitempty"`
	AutoReleaseLastError     string `json:"auto_release_last_error,omitempty"`
	AutoReleaseState         string `json:"auto_release_state,omitempty"`
	CreatedAt                string `json:"created_at"`
	UpdatedAt                string `json:"updated_at"`
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

type WebSettings struct {
	AuthSecret          string `json:"auth_secret,omitempty"`
	DefaultOwnerEmail   string `json:"default_owner_email,omitempty"`
	DefaultStatusFilter string `json:"default_status_filter,omitempty"`
	BackgroundConfirm   bool   `json:"background_confirm"`
	ShowReleased        bool   `json:"show_released"`
}

type PublicMember struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Email       string `json:"email"`
	Username    string `json:"username,omitempty"`
	Role        string `json:"role"`
	Enabled     bool   `json:"enabled"`
	HasPassword bool   `json:"has_password"`
	HasAPIToken bool   `json:"has_api_token"`
	APITokenAt  string `json:"api_token_at,omitempty"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type MemberWithAssignments struct {
	PublicMember
	Assignments []AppleAccountMember `json:"assignments"`
	Profiles    []string             `json:"profiles,omitempty"`
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
			return MemberData{Members: []Member{}, Assignments: []AppleAccountMember{}, ProfileOwners: []ProfileOwner{}, Profiles: []ManagedProfile{}, ProfileAccess: []ProfileAccess{}, Reminders: []ReleaseReminder{}, Events: []OperationEvent{}, Settings: defaultWebSettings()}, nil
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
		item := MemberWithAssignments{PublicMember: publicMember(member)}
		for _, assignment := range db.Assignments {
			if assignment.MemberID == member.ID {
				item.Assignments = append(item.Assignments, assignment)
			}
		}
		for _, access := range db.ProfileAccess {
			if access.MemberID == member.ID {
				item.Profiles = append(item.Profiles, access.ProfileName)
			}
		}
		sort.Strings(item.Profiles)
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
		Username:  email,
		Role:      role,
		Enabled:   true,
		CreatedAt: now,
		UpdatedAt: now,
	}
	db.Members = append(db.Members, member)
	return member, s.Save(db)
}

func (s MemberStore) AddMemberWithPassword(name, email, role, password string) (Member, error) {
	member, err := s.AddMember(name, email, role)
	if err != nil {
		return Member{}, err
	}
	if err := s.SetMemberPassword(member.Email, password); err != nil {
		return Member{}, err
	}
	db, err := s.Load()
	if err != nil {
		return Member{}, err
	}
	member, _ = findMemberByEmailOrUsername(db, member.Email)
	return member, nil
}

func (s MemberStore) SetupAdmin(name, email, password string) (Member, error) {
	name = strings.TrimSpace(name)
	email = normalizeEmail(email)
	if name == "" {
		return Member{}, errors.New("name is required")
	}
	if email == "" || !strings.Contains(email, "@") {
		return Member{}, errors.New("valid email is required")
	}
	if len(password) < 8 {
		return Member{}, errors.New("password must be at least 8 characters")
	}
	db, err := s.Load()
	if err != nil {
		return Member{}, err
	}
	now := s.normalize().Now().Format(time.RFC3339)
	member, ok := findMemberByEmailOrUsername(db, email)
	if ok {
		idx, _ := findMemberIndexByEmailOrUsername(db, email)
		db.Members[idx].Name = name
		db.Members[idx].Email = email
		db.Members[idx].Username = email
		db.Members[idx].Role = "admin"
		db.Members[idx].Enabled = true
		db.Members[idx].UpdatedAt = now
		member = db.Members[idx]
	} else {
		member = Member{
			ID:        "member-" + slugPart(email),
			Name:      name,
			Email:     email,
			Username:  email,
			Role:      "admin",
			Enabled:   true,
			CreatedAt: now,
			UpdatedAt: now,
		}
		db.Members = append(db.Members, member)
	}
	if err := s.Save(db); err != nil {
		return Member{}, err
	}
	if err := s.SetMemberPassword(email, password); err != nil {
		return Member{}, err
	}
	db, err = s.Load()
	if err != nil {
		return Member{}, err
	}
	member, _ = findMemberByEmailOrUsername(db, email)
	return member, nil
}

func (s MemberStore) SetMemberPassword(emailOrUsername, password string) error {
	if len(password) < 8 {
		return errors.New("password must be at least 8 characters")
	}
	db, err := s.Load()
	if err != nil {
		return err
	}
	idx, ok := findMemberIndexByEmailOrUsername(db, emailOrUsername)
	if !ok {
		return fmt.Errorf("member %s not found", emailOrUsername)
	}
	salt, err := randomToken(24)
	if err != nil {
		return err
	}
	db.Members[idx].PasswordSalt = salt
	db.Members[idx].PasswordHash = hashPassword(password, salt)
	db.Members[idx].UpdatedAt = s.normalize().Now().Format(time.RFC3339)
	return s.Save(db)
}

func (s MemberStore) UpdateMemberEmail(memberID, newEmail string) (Member, error) {
	newEmail = normalizeEmail(newEmail)
	if newEmail == "" || !strings.Contains(newEmail, "@") {
		return Member{}, errors.New("valid email is required")
	}
	db, err := s.Load()
	if err != nil {
		return Member{}, err
	}
	targetIdx := -1
	for i, member := range db.Members {
		if member.ID == memberID {
			targetIdx = i
			continue
		}
		if strings.EqualFold(member.Email, newEmail) || strings.EqualFold(member.Username, newEmail) {
			return Member{}, fmt.Errorf("member %s already exists", newEmail)
		}
	}
	if targetIdx < 0 {
		return Member{}, fmt.Errorf("member %s not found", memberID)
	}
	oldEmail := db.Members[targetIdx].Email
	db.Members[targetIdx].Email = newEmail
	db.Members[targetIdx].Username = newEmail
	db.Members[targetIdx].UpdatedAt = s.normalize().Now().Format(time.RFC3339)
	if strings.EqualFold(db.Settings.DefaultOwnerEmail, oldEmail) {
		db.Settings.DefaultOwnerEmail = newEmail
	}
	return db.Members[targetIdx], s.Save(db)
}

func (s MemberStore) UpdateMember(emailOrUsername, name, email, role string) (Member, error) {
	return updateMemberInStore(s, emailOrUsername, name, email, role)
}

func (s MemberStore) VerifyMemberPassword(emailOrUsername, password string) (Member, bool, error) {
	db, err := s.Load()
	if err != nil {
		return Member{}, false, err
	}
	member, ok := findMemberByEmailOrUsername(db, emailOrUsername)
	if !ok || !member.Enabled || member.PasswordHash == "" || member.PasswordSalt == "" {
		return Member{}, false, nil
	}
	got := hashPassword(password, member.PasswordSalt)
	if subtle.ConstantTimeCompare([]byte(got), []byte(member.PasswordHash)) != 1 {
		return Member{}, false, nil
	}
	return member, true, nil
}

func (s MemberStore) HasPasswordMembers() (bool, error) {
	db, err := s.Load()
	if err != nil {
		return false, err
	}
	for _, member := range db.Members {
		if member.Enabled && member.PasswordHash != "" && member.PasswordSalt != "" {
			return true, nil
		}
	}
	return false, nil
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

func (s MemberStore) MembersForApple(appleEmail string) ([]PublicMember, error) {
	appleEmail = normalizeEmail(appleEmail)
	db, err := s.Load()
	if err != nil {
		return nil, err
	}
	var members []PublicMember
	for _, assignment := range db.Assignments {
		if !strings.EqualFold(assignment.AppleEmail, appleEmail) {
			continue
		}
		if member, ok := findMemberByID(db, assignment.MemberID); ok {
			members = append(members, publicMember(member))
		}
	}
	return members, nil
}

func (s MemberStore) ProfileOwners() ([]PublicProfileOwner, error) {
	return profileOwnersInStore(s)
}

func (s MemberStore) ProfileOwner(profileName string) (PublicProfileOwner, bool, error) {
	return profileOwnerInStore(s, profileName)
}

func (s MemberStore) SetProfileOwner(profileName, memberEmail string) (PublicProfileOwner, error) {
	return setProfileOwnerInStore(s, profileName, memberEmail)
}

func (s MemberStore) ClearProfileOwner(profileName string) error {
	return clearProfileOwnerInStore(s, profileName)
}

func (s MemberStore) ListManagedProfiles(memberEmail string) ([]ManagedProfile, error) {
	return listManagedProfilesInStore(s, memberEmail)
}

func (s MemberStore) UpsertManagedProfile(profile Profile) (ManagedProfile, error) {
	return upsertManagedProfileInStore(s, profile)
}

func (s MemberStore) SetManagedProfileEnabled(profileName string, enabled bool) (ManagedProfile, error) {
	return setManagedProfileEnabledInStore(s, profileName, enabled)
}

func (s MemberStore) DeleteManagedProfile(profileName string) error {
	return deleteManagedProfileInStore(s, profileName)
}

func (s MemberStore) AssignProfileAccess(profileName, memberEmail string) (ProfileAccess, error) {
	return assignProfileAccessInStore(s, profileName, memberEmail)
}

func (s MemberStore) UnassignProfileAccess(profileName, memberEmail string) error {
	return unassignProfileAccessInStore(s, profileName, memberEmail)
}

func (s MemberStore) SetMemberProfileAccess(memberEmail string, profileNames []string) ([]ProfileAccess, error) {
	return setMemberProfileAccessInStore(s, memberEmail, profileNames)
}

func (s MemberStore) MembersForProfile(profileName string) ([]PublicMember, error) {
	return membersForProfileInStore(s, profileName)
}

func (s MemberStore) ListReleaseReminders(memberEmail string) ([]ReleaseReminder, error) {
	return listReleaseRemindersInStore(s, memberEmail)
}

func (s MemberStore) ReleaseReminder(profileName string) (ReleaseReminder, bool, error) {
	return releaseReminderInStore(s, profileName)
}

func (s MemberStore) UpsertReleaseReminder(reminder ReleaseReminder) (ReleaseReminder, error) {
	reminder = normalizeReleaseReminder(reminder)
	if reminder.ProfileName == "" {
		return ReleaseReminder{}, errors.New("profile is required")
	}
	return s.mutateReleaseReminders(func(db *MemberData, now string) (ReleaseReminder, error) {
		for i := range db.Reminders {
			if db.Reminders[i].ProfileName != reminder.ProfileName {
				continue
			}
			reminder = mergeReleaseReminderUpsert(db.Reminders[i], reminder, now)
			db.Reminders[i] = reminder
			return reminder, nil
		}
		if reminder.CreatedAt == "" {
			reminder.CreatedAt = now
		}
		reminder.UpdatedAt = now
		db.Reminders = append(db.Reminders, reminder)
		return reminder, nil
	})
}

func (s MemberStore) UpdateReleaseReminder(profileName string, update func(ReleaseReminder) (ReleaseReminder, error)) (ReleaseReminder, error) {
	profileName = strings.TrimSpace(profileName)
	if profileName == "" {
		return ReleaseReminder{}, errors.New("profile is required")
	}
	if update == nil {
		return ReleaseReminder{}, errors.New("release reminder update is required")
	}
	return s.mutateReleaseReminders(func(db *MemberData, now string) (ReleaseReminder, error) {
		for i := range db.Reminders {
			if db.Reminders[i].ProfileName != profileName {
				continue
			}
			updated, err := update(db.Reminders[i])
			if err != nil {
				return ReleaseReminder{}, err
			}
			updated = normalizeReleaseReminderCallback(db.Reminders[i], updated, now)
			db.Reminders[i] = updated
			return updated, nil
		}
		return ReleaseReminder{}, releaseReminderNotFoundError(profileName)
	})
}

func (s MemberStore) MarkReleaseReminderDue(profileName, notifiedAt string) (ReleaseReminder, error) {
	if notifiedAt == "" {
		notifiedAt = s.currentTime().Format(time.RFC3339)
	}
	return s.UpdateReleaseReminder(profileName, func(reminder ReleaseReminder) (ReleaseReminder, error) {
		reminder.Status = ReleaseReminderStatusDueNotified
		reminder.LastNotifiedAt = notifiedAt
		return reminder, nil
	})
}

func (s MemberStore) MarkReleaseReminderReleased(profileName, releasedAt string) (ReleaseReminder, error) {
	if releasedAt == "" {
		releasedAt = s.currentTime().Format(time.RFC3339)
	}
	return s.UpdateReleaseReminder(profileName, func(reminder ReleaseReminder) (ReleaseReminder, error) {
		reminder.Status = ReleaseReminderStatusReleased
		reminder.ReleasedAt = releasedAt
		return reminder, nil
	})
}

func (s MemberStore) mutateReleaseReminders(mutate func(*MemberData, string) (ReleaseReminder, error)) (ReleaseReminder, error) {
	s = s.normalize()
	path, err := ExpandPath(s.Path)
	if err != nil {
		return ReleaseReminder{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return ReleaseReminder{}, err
	}
	var result ReleaseReminder
	err = withFileLock(path+".lock", func() error {
		db, err := s.Load()
		if err != nil {
			return err
		}
		result, err = mutate(&db, s.currentTime().Format(time.RFC3339))
		if err != nil {
			return err
		}
		return s.Save(db)
	})
	if err != nil {
		return ReleaseReminder{}, err
	}
	return result, nil
}

func (s MemberStore) WebSettings() (WebSettings, error) {
	db, err := s.Load()
	if err != nil {
		return WebSettings{}, err
	}
	return publicWebSettings(db.Settings), nil
}

func (s MemberStore) UpdateWebSettings(update WebSettings) (WebSettings, error) {
	db, err := s.Load()
	if err != nil {
		return WebSettings{}, err
	}
	db.Settings.DefaultOwnerEmail = normalizeEmail(update.DefaultOwnerEmail)
	db.Settings.DefaultStatusFilter = strings.TrimSpace(update.DefaultStatusFilter)
	db.Settings.BackgroundConfirm = update.BackgroundConfirm
	db.Settings.ShowReleased = update.ShowReleased
	if err := s.Save(db); err != nil {
		return WebSettings{}, err
	}
	return publicWebSettings(db.Settings), nil
}

func (s MemberStore) EnsureAuthSecret() (string, error) {
	db, err := s.Load()
	if err != nil {
		return "", err
	}
	if db.Settings.AuthSecret == "" {
		secret, err := randomToken(32)
		if err != nil {
			return "", err
		}
		db.Settings.AuthSecret = secret
		if err := s.Save(db); err != nil {
			return "", err
		}
	}
	return db.Settings.AuthSecret, nil
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
	if db.ProfileOwners == nil {
		db.ProfileOwners = []ProfileOwner{}
	}
	if db.Profiles == nil {
		db.Profiles = []ManagedProfile{}
	}
	if db.ProfileAccess == nil {
		db.ProfileAccess = []ProfileAccess{}
	}
	if db.Reminders == nil {
		db.Reminders = []ReleaseReminder{}
	}
	if db.Events == nil {
		db.Events = []OperationEvent{}
	}
	if db.Settings.AuthSecret == "" {
		db.Settings.BackgroundConfirm = true
	}
	for i := range db.Members {
		if db.Members[i].Username == "" {
			db.Members[i].Username = db.Members[i].Email
		}
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

func findMemberByEmailOrUsername(db MemberData, emailOrUsername string) (Member, bool) {
	idx, ok := findMemberIndexByEmailOrUsername(db, emailOrUsername)
	if !ok {
		return Member{}, false
	}
	return db.Members[idx], true
}

func findMemberIndexByEmailOrUsername(db MemberData, emailOrUsername string) (int, bool) {
	value := normalizeEmail(emailOrUsername)
	for i, member := range db.Members {
		if strings.EqualFold(member.Email, value) || strings.EqualFold(member.Username, value) {
			return i, true
		}
	}
	return 0, false
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

func findManagedProfileByName(db MemberData, name string) (ManagedProfile, bool) {
	for _, profile := range db.Profiles {
		if profile.Name == name {
			return profile, true
		}
	}
	return ManagedProfile{}, false
}

func publicMember(member Member) PublicMember {
	return PublicMember{
		ID:          member.ID,
		Name:        member.Name,
		Email:       member.Email,
		Username:    member.Username,
		Role:        member.Role,
		Enabled:     member.Enabled,
		HasPassword: member.PasswordHash != "" && member.PasswordSalt != "",
		HasAPIToken: member.APITokenHash != "",
		APITokenAt:  member.APITokenAt,
		CreatedAt:   member.CreatedAt,
		UpdatedAt:   member.UpdatedAt,
	}
}

func defaultWebSettings() WebSettings {
	return WebSettings{BackgroundConfirm: true}
}

func publicWebSettings(settings WebSettings) WebSettings {
	settings.AuthSecret = ""
	return settings
}

func randomToken(bytesLen int) (string, error) {
	buf := make([]byte, bytesLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func hashPassword(password, salt string) string {
	sum := []byte(salt + "\x00" + password)
	for i := 0; i < 120000; i++ {
		hash := sha256.Sum256(sum)
		sum = hash[:]
	}
	return base64.RawURLEncoding.EncodeToString(sum)
}

type memberDataStore interface {
	Load() (MemberData, error)
	Save(MemberData) error
	currentTime() time.Time
}

func addMemberToStore(s memberDataStore, name, email, role string) (Member, error) {
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
	now := s.currentTime().Format(time.RFC3339)
	member := Member{
		ID:        "member-" + slugPart(email),
		Name:      name,
		Email:     email,
		Username:  email,
		Role:      role,
		Enabled:   true,
		CreatedAt: now,
		UpdatedAt: now,
	}
	db.Members = append(db.Members, member)
	return member, s.Save(db)
}

func setupAdminInStore(s memberDataStore, name, email, password string) (Member, error) {
	name = strings.TrimSpace(name)
	email = normalizeEmail(email)
	if name == "" {
		return Member{}, errors.New("name is required")
	}
	if email == "" || !strings.Contains(email, "@") {
		return Member{}, errors.New("valid email is required")
	}
	if len(password) < 8 {
		return Member{}, errors.New("password must be at least 8 characters")
	}
	db, err := s.Load()
	if err != nil {
		return Member{}, err
	}
	now := s.currentTime().Format(time.RFC3339)
	member, ok := findMemberByEmailOrUsername(db, email)
	if ok {
		idx, _ := findMemberIndexByEmailOrUsername(db, email)
		db.Members[idx].Name = name
		db.Members[idx].Email = email
		db.Members[idx].Username = email
		db.Members[idx].Role = "admin"
		db.Members[idx].Enabled = true
		db.Members[idx].UpdatedAt = now
		member = db.Members[idx]
	} else {
		member = Member{
			ID:        "member-" + slugPart(email),
			Name:      name,
			Email:     email,
			Username:  email,
			Role:      "admin",
			Enabled:   true,
			CreatedAt: now,
			UpdatedAt: now,
		}
		db.Members = append(db.Members, member)
	}
	if err := s.Save(db); err != nil {
		return Member{}, err
	}
	if err := setMemberPasswordInStore(s, email, password); err != nil {
		return Member{}, err
	}
	db, err = s.Load()
	if err != nil {
		return Member{}, err
	}
	member, _ = findMemberByEmailOrUsername(db, email)
	return member, nil
}

func setMemberPasswordInStore(s memberDataStore, emailOrUsername, password string) error {
	if len(password) < 8 {
		return errors.New("password must be at least 8 characters")
	}
	db, err := s.Load()
	if err != nil {
		return err
	}
	idx, ok := findMemberIndexByEmailOrUsername(db, emailOrUsername)
	if !ok {
		return fmt.Errorf("member %s not found", emailOrUsername)
	}
	salt, err := randomToken(24)
	if err != nil {
		return err
	}
	db.Members[idx].PasswordSalt = salt
	db.Members[idx].PasswordHash = hashPassword(password, salt)
	db.Members[idx].UpdatedAt = s.currentTime().Format(time.RFC3339)
	return s.Save(db)
}

func updateMemberEmailInStore(s memberDataStore, memberID, newEmail string) (Member, error) {
	newEmail = normalizeEmail(newEmail)
	if newEmail == "" || !strings.Contains(newEmail, "@") {
		return Member{}, errors.New("valid email is required")
	}
	db, err := s.Load()
	if err != nil {
		return Member{}, err
	}
	targetIdx := -1
	for i, member := range db.Members {
		if member.ID == memberID {
			targetIdx = i
			continue
		}
		if strings.EqualFold(member.Email, newEmail) || strings.EqualFold(member.Username, newEmail) {
			return Member{}, fmt.Errorf("member %s already exists", newEmail)
		}
	}
	if targetIdx < 0 {
		return Member{}, fmt.Errorf("member %s not found", memberID)
	}
	oldEmail := db.Members[targetIdx].Email
	db.Members[targetIdx].Email = newEmail
	db.Members[targetIdx].Username = newEmail
	db.Members[targetIdx].UpdatedAt = s.currentTime().Format(time.RFC3339)
	if strings.EqualFold(db.Settings.DefaultOwnerEmail, oldEmail) {
		db.Settings.DefaultOwnerEmail = newEmail
	}
	return db.Members[targetIdx], s.Save(db)
}

func updateMemberInStore(s memberDataStore, emailOrUsername, name, email, role string) (Member, error) {
	emailOrUsername = strings.TrimSpace(emailOrUsername)
	name = strings.TrimSpace(name)
	email = normalizeEmail(email)
	role = normalizeMemberRole(role)
	if emailOrUsername == "" {
		return Member{}, errors.New("member email is required")
	}
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
	targetIdx := -1
	for i, member := range db.Members {
		if strings.EqualFold(member.Email, emailOrUsername) || strings.EqualFold(member.Username, emailOrUsername) || member.ID == emailOrUsername {
			targetIdx = i
			continue
		}
		if strings.EqualFold(member.Email, email) || strings.EqualFold(member.Username, email) {
			return Member{}, fmt.Errorf("member %s already exists", email)
		}
	}
	if targetIdx < 0 {
		return Member{}, fmt.Errorf("member %s not found", emailOrUsername)
	}
	oldEmail := db.Members[targetIdx].Email
	db.Members[targetIdx].Name = name
	db.Members[targetIdx].Email = email
	db.Members[targetIdx].Username = email
	db.Members[targetIdx].Role = role
	db.Members[targetIdx].UpdatedAt = s.currentTime().Format(time.RFC3339)
	if strings.EqualFold(db.Settings.DefaultOwnerEmail, oldEmail) {
		db.Settings.DefaultOwnerEmail = email
	}
	return db.Members[targetIdx], s.Save(db)
}

func verifyMemberPasswordInStore(s memberDataStore, emailOrUsername, password string) (Member, bool, error) {
	db, err := s.Load()
	if err != nil {
		return Member{}, false, err
	}
	member, ok := findMemberByEmailOrUsername(db, emailOrUsername)
	if !ok || !member.Enabled || member.PasswordHash == "" || member.PasswordSalt == "" {
		return Member{}, false, nil
	}
	got := hashPassword(password, member.PasswordSalt)
	if subtle.ConstantTimeCompare([]byte(got), []byte(member.PasswordHash)) != 1 {
		return Member{}, false, nil
	}
	return member, true, nil
}

func hasPasswordMembersInStore(s memberDataStore) (bool, error) {
	db, err := s.Load()
	if err != nil {
		return false, err
	}
	for _, member := range db.Members {
		if member.Enabled && member.PasswordHash != "" && member.PasswordSalt != "" {
			return true, nil
		}
	}
	return false, nil
}

func setMemberEnabledInStore(s memberDataStore, email string, enabled bool) (Member, error) {
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
	db.Members[idx].UpdatedAt = s.currentTime().Format(time.RFC3339)
	return db.Members[idx], s.Save(db)
}

func assignMemberInStore(s memberDataStore, appleEmail, memberEmail, relation string) (AppleAccountMember, error) {
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
		CreatedAt:  s.currentTime().Format(time.RFC3339),
	}
	db.Assignments = append(db.Assignments, assignment)
	return assignment, s.Save(db)
}

func unassignMemberInStore(s memberDataStore, appleEmail, memberEmail string) error {
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

func membersForAppleInStore(s memberDataStore, appleEmail string) ([]PublicMember, error) {
	appleEmail = normalizeEmail(appleEmail)
	db, err := s.Load()
	if err != nil {
		return nil, err
	}
	var members []PublicMember
	for _, assignment := range db.Assignments {
		if !strings.EqualFold(assignment.AppleEmail, appleEmail) {
			continue
		}
		if member, ok := findMemberByID(db, assignment.MemberID); ok {
			members = append(members, publicMember(member))
		}
	}
	return members, nil
}

func profileOwnersInStore(s memberDataStore) ([]PublicProfileOwner, error) {
	db, err := s.Load()
	if err != nil {
		return nil, err
	}
	out := make([]PublicProfileOwner, 0, len(db.ProfileOwners))
	for _, owner := range db.ProfileOwners {
		if member, ok := findMemberByID(db, owner.MemberID); ok {
			out = append(out, PublicProfileOwner{
				ProfileName: owner.ProfileName,
				Owner:       publicMember(member),
				UpdatedAt:   owner.UpdatedAt,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].ProfileName) < strings.ToLower(out[j].ProfileName)
	})
	return out, nil
}

func profileOwnerInStore(s memberDataStore, profileName string) (PublicProfileOwner, bool, error) {
	profileName = strings.TrimSpace(profileName)
	if profileName == "" {
		return PublicProfileOwner{}, false, nil
	}
	db, err := s.Load()
	if err != nil {
		return PublicProfileOwner{}, false, err
	}
	for _, owner := range db.ProfileOwners {
		if owner.ProfileName != profileName {
			continue
		}
		member, ok := findMemberByID(db, owner.MemberID)
		if !ok {
			return PublicProfileOwner{}, false, nil
		}
		return PublicProfileOwner{
			ProfileName: owner.ProfileName,
			Owner:       publicMember(member),
			UpdatedAt:   owner.UpdatedAt,
		}, true, nil
	}
	return PublicProfileOwner{}, false, nil
}

func setProfileOwnerInStore(s memberDataStore, profileName, memberEmail string) (PublicProfileOwner, error) {
	profileName = strings.TrimSpace(profileName)
	memberEmail = normalizeEmail(memberEmail)
	if profileName == "" {
		return PublicProfileOwner{}, errors.New("profile is required")
	}
	if memberEmail == "" || !strings.Contains(memberEmail, "@") {
		return PublicProfileOwner{}, errors.New("valid member email is required")
	}
	db, err := s.Load()
	if err != nil {
		return PublicProfileOwner{}, err
	}
	member, ok := findMemberByEmail(db, memberEmail)
	if !ok {
		return PublicProfileOwner{}, fmt.Errorf("member %s not found", memberEmail)
	}
	record := ProfileOwner{
		ProfileName: profileName,
		MemberID:    member.ID,
		UpdatedAt:   s.currentTime().Format(time.RFC3339),
	}
	updated := false
	for i := range db.ProfileOwners {
		if db.ProfileOwners[i].ProfileName == profileName {
			db.ProfileOwners[i] = record
			updated = true
			break
		}
	}
	if !updated {
		db.ProfileOwners = append(db.ProfileOwners, record)
	}
	if err := s.Save(db); err != nil {
		return PublicProfileOwner{}, err
	}
	return PublicProfileOwner{
		ProfileName: record.ProfileName,
		Owner:       publicMember(member),
		UpdatedAt:   record.UpdatedAt,
	}, nil
}

func clearProfileOwnerInStore(s memberDataStore, profileName string) error {
	profileName = strings.TrimSpace(profileName)
	if profileName == "" {
		return errors.New("profile is required")
	}
	db, err := s.Load()
	if err != nil {
		return err
	}
	out := db.ProfileOwners[:0]
	for _, owner := range db.ProfileOwners {
		if owner.ProfileName != profileName {
			out = append(out, owner)
		}
	}
	db.ProfileOwners = out
	return s.Save(db)
}

func listManagedProfilesInStore(s memberDataStore, memberEmail string) ([]ManagedProfile, error) {
	memberEmail = normalizeEmail(memberEmail)
	db, err := s.Load()
	if err != nil {
		return nil, err
	}
	var member Member
	isAdmin := false
	if memberEmail != "" {
		if found, ok := findMemberByEmailOrUsername(db, memberEmail); ok {
			member = found
			isAdmin = found.Role == "admin"
		}
	}
	out := make([]ManagedProfile, 0, len(db.Profiles))
	for _, profile := range db.Profiles {
		if !profile.Enabled && !isAdmin {
			continue
		}
		if isAdmin || memberEmail == "" {
			out = append(out, profile)
			continue
		}
		for _, access := range db.ProfileAccess {
			if access.ProfileName == profile.Name && access.MemberID == member.ID {
				out = append(out, profile)
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

func upsertManagedProfileInStore(s memberDataStore, profile Profile) (ManagedProfile, error) {
	profile.Name = strings.TrimSpace(profile.Name)
	if profile.Name == "" {
		return ManagedProfile{}, errors.New("profile name is required")
	}
	if profile.AWS.AccountEmail == "" && profile.AWS.ElasticIPOwnerTag.Value != "" {
		profile.AWS.AccountEmail = profile.AWS.ElasticIPOwnerTag.Value
	}
	if profile.Description == "" && profile.AWS.AccountEmail != "" {
		profile.Description = "Apple account: " + profile.AWS.AccountEmail
	}
	if profile.AWS.ElasticIPOwnerTag.Key == "" && profile.AWS.AccountEmail != "" {
		profile.AWS.ElasticIPOwnerTag = AWSTagConfig{Key: "Apple", Value: profile.AWS.AccountEmail}
	}
	db, err := s.Load()
	if err != nil {
		return ManagedProfile{}, err
	}
	now := s.currentTime().Format(time.RFC3339)
	record := ManagedProfile{
		Name:        profile.Name,
		AppleEmail:  profile.AWS.AccountEmail,
		Enabled:     true,
		ProfileYAML: FormatProfileFile(profile),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	for i := range db.Profiles {
		if db.Profiles[i].Name == profile.Name {
			record.CreatedAt = db.Profiles[i].CreatedAt
			if record.CreatedAt == "" {
				record.CreatedAt = now
			}
			record.Enabled = db.Profiles[i].Enabled
			db.Profiles[i] = record
			return record, s.Save(db)
		}
	}
	db.Profiles = append(db.Profiles, record)
	return record, s.Save(db)
}

func setManagedProfileEnabledInStore(s memberDataStore, profileName string, enabled bool) (ManagedProfile, error) {
	profileName = strings.TrimSpace(profileName)
	if profileName == "" {
		return ManagedProfile{}, errors.New("profile is required")
	}
	db, err := s.Load()
	if err != nil {
		return ManagedProfile{}, err
	}
	for i := range db.Profiles {
		if db.Profiles[i].Name == profileName {
			db.Profiles[i].Enabled = enabled
			db.Profiles[i].UpdatedAt = s.currentTime().Format(time.RFC3339)
			if err := s.Save(db); err != nil {
				return ManagedProfile{}, err
			}
			return db.Profiles[i], nil
		}
	}
	return ManagedProfile{}, fmt.Errorf("profile %s not found", profileName)
}

func deleteManagedProfileInStore(s memberDataStore, profileName string) error {
	profileName = strings.TrimSpace(profileName)
	if profileName == "" {
		return errors.New("profile is required")
	}
	db, err := s.Load()
	if err != nil {
		return err
	}
	profiles := db.Profiles[:0]
	removed := false
	for _, profile := range db.Profiles {
		if profile.Name == profileName {
			removed = true
			continue
		}
		profiles = append(profiles, profile)
	}
	if !removed {
		return fmt.Errorf("profile %s not found", profileName)
	}
	db.Profiles = profiles
	access := db.ProfileAccess[:0]
	for _, item := range db.ProfileAccess {
		if item.ProfileName != profileName {
			access = append(access, item)
		}
	}
	db.ProfileAccess = access
	return s.Save(db)
}

func assignProfileAccessInStore(s memberDataStore, profileName, memberEmail string) (ProfileAccess, error) {
	profileName = strings.TrimSpace(profileName)
	memberEmail = normalizeEmail(memberEmail)
	if profileName == "" {
		return ProfileAccess{}, errors.New("profile is required")
	}
	db, err := s.Load()
	if err != nil {
		return ProfileAccess{}, err
	}
	if _, ok := findManagedProfileByName(db, profileName); !ok {
		return ProfileAccess{}, fmt.Errorf("profile %s not found", profileName)
	}
	member, ok := findMemberByEmail(db, memberEmail)
	if !ok {
		return ProfileAccess{}, fmt.Errorf("member %s not found", memberEmail)
	}
	for _, access := range db.ProfileAccess {
		if access.ProfileName == profileName && access.MemberID == member.ID {
			return access, nil
		}
	}
	access := ProfileAccess{ProfileName: profileName, MemberID: member.ID, CreatedAt: s.currentTime().Format(time.RFC3339)}
	db.ProfileAccess = append(db.ProfileAccess, access)
	return access, s.Save(db)
}

func unassignProfileAccessInStore(s memberDataStore, profileName, memberEmail string) error {
	profileName = strings.TrimSpace(profileName)
	memberEmail = normalizeEmail(memberEmail)
	db, err := s.Load()
	if err != nil {
		return err
	}
	member, ok := findMemberByEmail(db, memberEmail)
	if !ok {
		return fmt.Errorf("member %s not found", memberEmail)
	}
	out := db.ProfileAccess[:0]
	removed := false
	for _, access := range db.ProfileAccess {
		if access.ProfileName == profileName && access.MemberID == member.ID {
			removed = true
			continue
		}
		out = append(out, access)
	}
	if !removed {
		return fmt.Errorf("profile access not found")
	}
	db.ProfileAccess = out
	return s.Save(db)
}

func setMemberProfileAccessInStore(s memberDataStore, memberEmail string, profileNames []string) ([]ProfileAccess, error) {
	memberEmail = normalizeEmail(memberEmail)
	if memberEmail == "" {
		return nil, errors.New("member email is required")
	}
	db, err := s.Load()
	if err != nil {
		return nil, err
	}
	member, ok := findMemberByEmail(db, memberEmail)
	if !ok {
		return nil, fmt.Errorf("member %s not found", memberEmail)
	}
	desired := map[string]bool{}
	for _, name := range profileNames {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := findManagedProfileByName(db, name); !ok {
			return nil, fmt.Errorf("profile %s not found", name)
		}
		desired[name] = true
	}
	now := s.currentTime().Format(time.RFC3339)
	out := db.ProfileAccess[:0]
	existing := map[string]ProfileAccess{}
	for _, access := range db.ProfileAccess {
		if access.MemberID != member.ID {
			out = append(out, access)
			continue
		}
		if desired[access.ProfileName] {
			existing[access.ProfileName] = access
			out = append(out, access)
		}
	}
	result := make([]ProfileAccess, 0, len(desired))
	for profileName := range desired {
		access, ok := existing[profileName]
		if !ok {
			access = ProfileAccess{ProfileName: profileName, MemberID: member.ID, CreatedAt: now}
			out = append(out, access)
		}
		result = append(result, access)
	}
	db.ProfileAccess = out
	sort.Slice(result, func(i, j int) bool {
		return strings.ToLower(result[i].ProfileName) < strings.ToLower(result[j].ProfileName)
	})
	return result, s.Save(db)
}

func membersForProfileInStore(s memberDataStore, profileName string) ([]PublicMember, error) {
	profileName = strings.TrimSpace(profileName)
	db, err := s.Load()
	if err != nil {
		return nil, err
	}
	var members []PublicMember
	for _, access := range db.ProfileAccess {
		if access.ProfileName != profileName {
			continue
		}
		if member, ok := findMemberByID(db, access.MemberID); ok {
			members = append(members, publicMember(member))
		}
	}
	sort.Slice(members, func(i, j int) bool {
		return strings.ToLower(members[i].Email) < strings.ToLower(members[j].Email)
	})
	return members, nil
}

func listReleaseRemindersInStore(s memberDataStore, memberEmail string) ([]ReleaseReminder, error) {
	memberEmail = normalizeEmail(memberEmail)
	db, err := s.Load()
	if err != nil {
		return nil, err
	}
	var member Member
	isAdmin := false
	if memberEmail != "" {
		if found, ok := findMemberByEmailOrUsername(db, memberEmail); ok {
			member = found
			isAdmin = found.Role == "admin"
		}
	}
	out := make([]ReleaseReminder, 0, len(db.Reminders))
	for _, reminder := range db.Reminders {
		if isAdmin || memberEmail == "" || memberHasProfileAccess(db, member, reminder.ProfileName) {
			out = append(out, reminder)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].ProfileName) < strings.ToLower(out[j].ProfileName)
	})
	return out, nil
}

func releaseReminderInStore(s memberDataStore, profileName string) (ReleaseReminder, bool, error) {
	profileName = strings.TrimSpace(profileName)
	if profileName == "" {
		return ReleaseReminder{}, false, nil
	}
	db, err := s.Load()
	if err != nil {
		return ReleaseReminder{}, false, err
	}
	for _, reminder := range db.Reminders {
		if reminder.ProfileName == profileName {
			return reminder, true, nil
		}
	}
	return ReleaseReminder{}, false, nil
}

func normalizeReleaseReminder(reminder ReleaseReminder) ReleaseReminder {
	reminder.ProfileName = strings.TrimSpace(reminder.ProfileName)
	reminder.AppleEmail = normalizeEmail(reminder.AppleEmail)
	reminder.OwnerEmail = normalizeEmail(reminder.OwnerEmail)
	reminder.LastExtendedByEmail = normalizeEmail(reminder.LastExtendedByEmail)
	if reminder.Status == "" {
		reminder.Status = ReleaseReminderStatusActive
	}
	return reminder
}

func normalizeReleaseReminderCallback(current, updated ReleaseReminder, now string) ReleaseReminder {
	updated.ProfileName = current.ProfileName
	updated.CreatedAt = current.CreatedAt
	updated = normalizeReleaseReminder(updated)
	updated.UpdatedAt = now
	return updated
}

func mergeReleaseReminderUpsert(current, updated ReleaseReminder, now string) ReleaseReminder {
	updated = normalizeReleaseReminderCallback(current, updated, now)
	updated.AutoReleaseEnabled = current.AutoReleaseEnabled
	updated.AutoReleaseAt = current.AutoReleaseAt
	updated.AutoReleaseStartedAt = current.AutoReleaseStartedAt
	updated.AutoReleaseLastAttemptAt = current.AutoReleaseLastAttemptAt
	updated.AutoReleaseAttempts = current.AutoReleaseAttempts
	updated.AutoReleaseLastError = current.AutoReleaseLastError
	updated.AutoReleaseState = current.AutoReleaseState
	return updated
}

func releaseReminderNotFoundError(profileName string) error {
	return fmt.Errorf("release reminder for profile %s not found", profileName)
}

func memberHasProfileAccess(db MemberData, member Member, profileName string) bool {
	if member.ID == "" {
		return false
	}
	for _, access := range db.ProfileAccess {
		if access.ProfileName == profileName && access.MemberID == member.ID {
			return true
		}
	}
	return false
}

func webSettingsInStore(s memberDataStore) (WebSettings, error) {
	db, err := s.Load()
	if err != nil {
		return WebSettings{}, err
	}
	return publicWebSettings(db.Settings), nil
}

func updateWebSettingsInStore(s memberDataStore, update WebSettings) (WebSettings, error) {
	db, err := s.Load()
	if err != nil {
		return WebSettings{}, err
	}
	db.Settings.DefaultOwnerEmail = normalizeEmail(update.DefaultOwnerEmail)
	db.Settings.DefaultStatusFilter = strings.TrimSpace(update.DefaultStatusFilter)
	db.Settings.BackgroundConfirm = update.BackgroundConfirm
	db.Settings.ShowReleased = update.ShowReleased
	if err := s.Save(db); err != nil {
		return WebSettings{}, err
	}
	return publicWebSettings(db.Settings), nil
}

func ensureAuthSecretInStore(s memberDataStore) (string, error) {
	db, err := s.Load()
	if err != nil {
		return "", err
	}
	if db.Settings.AuthSecret == "" {
		secret, err := randomToken(32)
		if err != nil {
			return "", err
		}
		db.Settings.AuthSecret = secret
		if err := s.Save(db); err != nil {
			return "", err
		}
	}
	return db.Settings.AuthSecret, nil
}

func recordEventInStore(s memberDataStore, event OperationEvent) error {
	db, err := s.Load()
	if err != nil {
		return err
	}
	now := s.currentTime().Format(time.RFC3339)
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

func recentEventsInStore(s memberDataStore, appleEmail string, limit int) ([]OperationEvent, error) {
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

func (s MemberStore) currentTime() time.Time {
	return s.normalize().Now()
}
