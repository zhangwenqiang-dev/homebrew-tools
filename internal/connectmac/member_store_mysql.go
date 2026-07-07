package connectmac

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

type MySQLMemberStore struct {
	DSN string
	Now func() time.Time
}

func NewMySQLMemberStoreFromEnv() (MySQLMemberStore, bool, error) {
	host := strings.TrimSpace(os.Getenv("CONNECTMAC_DB_HOST"))
	port := strings.TrimSpace(os.Getenv("CONNECTMAC_DB_PORT"))
	database := strings.TrimSpace(os.Getenv("CONNECTMAC_DB_DATABASE"))
	username := strings.TrimSpace(os.Getenv("CONNECTMAC_DB_USERNAME"))
	password := os.Getenv("CONNECTMAC_DB_PASSWORD")
	if host == "" && database == "" && username == "" && password == "" {
		return MySQLMemberStore{}, false, nil
	}
	if host == "" {
		host = "127.0.0.1"
	}
	if port == "" {
		port = "3306"
	}
	if database == "" || username == "" {
		return MySQLMemberStore{}, true, errors.New("CONNECTMAC_DB_DATABASE and CONNECTMAC_DB_USERNAME are required when MySQL member store is configured")
	}
	dsn := (&mysql.Config{
		User:                 username,
		Passwd:               password,
		Net:                  "tcp",
		Addr:                 host + ":" + port,
		DBName:               database,
		ParseTime:            true,
		Collation:            "utf8mb4_unicode_ci",
		AllowNativePasswords: true,
	}).FormatDSN()
	return MySQLMemberStore{DSN: dsn, Now: time.Now}, true, nil
}

func (s MySQLMemberStore) normalize() MySQLMemberStore {
	if s.Now == nil {
		s.Now = time.Now
	}
	return s
}

func (s MySQLMemberStore) open() (*sql.DB, error) {
	if strings.TrimSpace(s.DSN) == "" {
		return nil, errors.New("mysql dsn is empty")
	}
	db, err := sql.Open("mysql", s.DSN)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(5 * time.Minute)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func (s MySQLMemberStore) EnsureSchema() error {
	db, err := s.open()
	if err != nil {
		return err
	}
	defer db.Close()
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS cm_members (
			id VARCHAR(128) PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			email VARCHAR(255) NOT NULL UNIQUE,
			username VARCHAR(255) NOT NULL UNIQUE,
			role VARCHAR(32) NOT NULL,
			enabled BOOLEAN NOT NULL DEFAULT TRUE,
			password_hash TEXT NULL,
			password_salt TEXT NULL,
			created_at VARCHAR(64) NOT NULL,
			updated_at VARCHAR(64) NOT NULL
		) CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci`,
		`CREATE TABLE IF NOT EXISTS cm_assignments (
			apple_email VARCHAR(255) NOT NULL,
			member_id VARCHAR(128) NOT NULL,
			relation VARCHAR(64) NOT NULL,
			created_at VARCHAR(64) NOT NULL,
			PRIMARY KEY (apple_email, member_id),
			INDEX idx_cm_assignments_member_id (member_id)
		) CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci`,
		`CREATE TABLE IF NOT EXISTS cm_profile_owners (
			profile_name VARCHAR(255) PRIMARY KEY,
			member_id VARCHAR(128) NOT NULL,
			updated_at VARCHAR(64) NOT NULL,
			INDEX idx_cm_profile_owners_member_id (member_id)
		) CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci`,
		`CREATE TABLE IF NOT EXISTS cm_profiles (
			name VARCHAR(255) PRIMARY KEY,
			apple_email VARCHAR(255) NULL,
			enabled BOOLEAN NOT NULL DEFAULT TRUE,
			profile_yaml MEDIUMTEXT NOT NULL,
			created_at VARCHAR(64) NOT NULL,
			updated_at VARCHAR(64) NOT NULL,
			INDEX idx_cm_profiles_apple_email (apple_email)
		) CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci`,
		`CREATE TABLE IF NOT EXISTS cm_profile_members (
			profile_name VARCHAR(255) NOT NULL,
			member_id VARCHAR(128) NOT NULL,
			created_at VARCHAR(64) NOT NULL,
			PRIMARY KEY (profile_name, member_id),
			INDEX idx_cm_profile_members_member_id (member_id)
		) CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci`,
		`CREATE TABLE IF NOT EXISTS cm_settings (
			setting_key VARCHAR(128) PRIMARY KEY,
			setting_value TEXT NULL
		) CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci`,
		`CREATE TABLE IF NOT EXISTS cm_events (
			id VARCHAR(160) PRIMARY KEY,
			action VARCHAR(128) NOT NULL,
			profile VARCHAR(255) NOT NULL,
			apple_email VARCHAR(255) NULL,
			member_id VARCHAR(128) NULL,
			confirmed BOOLEAN NOT NULL DEFAULT FALSE,
			status VARCHAR(64) NOT NULL,
			message TEXT NULL,
			created_at VARCHAR(64) NOT NULL,
			INDEX idx_cm_events_apple_email_created_at (apple_email, created_at),
			INDEX idx_cm_events_created_at (created_at)
		) CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s MySQLMemberStore) Load() (MemberData, error) {
	if err := s.EnsureSchema(); err != nil {
		return MemberData{}, err
	}
	db, err := s.open()
	if err != nil {
		return MemberData{}, err
	}
	defer db.Close()
	out := MemberData{Members: []Member{}, Assignments: []AppleAccountMember{}, ProfileOwners: []ProfileOwner{}, Profiles: []ManagedProfile{}, ProfileAccess: []ProfileAccess{}, Events: []OperationEvent{}, Settings: defaultWebSettings()}
	rows, err := db.Query(`SELECT id, name, email, username, role, enabled, COALESCE(password_hash, ''), COALESCE(password_salt, ''), created_at, updated_at FROM cm_members ORDER BY email`)
	if err != nil {
		return MemberData{}, err
	}
	for rows.Next() {
		var member Member
		if err := rows.Scan(&member.ID, &member.Name, &member.Email, &member.Username, &member.Role, &member.Enabled, &member.PasswordHash, &member.PasswordSalt, &member.CreatedAt, &member.UpdatedAt); err != nil {
			rows.Close()
			return MemberData{}, err
		}
		out.Members = append(out.Members, member)
	}
	if err := rows.Close(); err != nil {
		return MemberData{}, err
	}
	rows, err = db.Query(`SELECT apple_email, member_id, relation, created_at FROM cm_assignments ORDER BY apple_email, member_id`)
	if err != nil {
		return MemberData{}, err
	}
	for rows.Next() {
		var assignment AppleAccountMember
		if err := rows.Scan(&assignment.AppleEmail, &assignment.MemberID, &assignment.Relation, &assignment.CreatedAt); err != nil {
			rows.Close()
			return MemberData{}, err
		}
		out.Assignments = append(out.Assignments, assignment)
	}
	if err := rows.Close(); err != nil {
		return MemberData{}, err
	}
	rows, err = db.Query(`SELECT profile_name, member_id, updated_at FROM cm_profile_owners ORDER BY profile_name`)
	if err != nil {
		return MemberData{}, err
	}
	for rows.Next() {
		var owner ProfileOwner
		if err := rows.Scan(&owner.ProfileName, &owner.MemberID, &owner.UpdatedAt); err != nil {
			rows.Close()
			return MemberData{}, err
		}
		out.ProfileOwners = append(out.ProfileOwners, owner)
	}
	if err := rows.Close(); err != nil {
		return MemberData{}, err
	}
	rows, err = db.Query(`SELECT name, COALESCE(apple_email, ''), enabled, profile_yaml, created_at, updated_at FROM cm_profiles ORDER BY name`)
	if err != nil {
		return MemberData{}, err
	}
	for rows.Next() {
		var profile ManagedProfile
		if err := rows.Scan(&profile.Name, &profile.AppleEmail, &profile.Enabled, &profile.ProfileYAML, &profile.CreatedAt, &profile.UpdatedAt); err != nil {
			rows.Close()
			return MemberData{}, err
		}
		out.Profiles = append(out.Profiles, profile)
	}
	if err := rows.Close(); err != nil {
		return MemberData{}, err
	}
	rows, err = db.Query(`SELECT profile_name, member_id, created_at FROM cm_profile_members ORDER BY profile_name, member_id`)
	if err != nil {
		return MemberData{}, err
	}
	for rows.Next() {
		var access ProfileAccess
		if err := rows.Scan(&access.ProfileName, &access.MemberID, &access.CreatedAt); err != nil {
			rows.Close()
			return MemberData{}, err
		}
		out.ProfileAccess = append(out.ProfileAccess, access)
	}
	if err := rows.Close(); err != nil {
		return MemberData{}, err
	}
	rows, err = db.Query(`SELECT setting_key, COALESCE(setting_value, '') FROM cm_settings`)
	if err != nil {
		return MemberData{}, err
	}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			rows.Close()
			return MemberData{}, err
		}
		switch key {
		case "auth_secret":
			out.Settings.AuthSecret = value
		case "default_owner_email":
			out.Settings.DefaultOwnerEmail = value
		case "default_status_filter":
			out.Settings.DefaultStatusFilter = value
		case "background_confirm":
			out.Settings.BackgroundConfirm = value == "true"
		case "show_released":
			out.Settings.ShowReleased = value == "true"
		}
	}
	if err := rows.Close(); err != nil {
		return MemberData{}, err
	}
	rows, err = db.Query(`SELECT id, action, profile, COALESCE(apple_email, ''), COALESCE(member_id, ''), confirmed, status, COALESCE(message, ''), created_at FROM cm_events ORDER BY created_at ASC LIMIT 500`)
	if err != nil {
		return MemberData{}, err
	}
	for rows.Next() {
		var event OperationEvent
		if err := rows.Scan(&event.ID, &event.Action, &event.Profile, &event.AppleEmail, &event.MemberID, &event.Confirmed, &event.Status, &event.Message, &event.CreatedAt); err != nil {
			rows.Close()
			return MemberData{}, err
		}
		out.Events = append(out.Events, event)
	}
	if err := rows.Close(); err != nil {
		return MemberData{}, err
	}
	normalizeMemberData(&out)
	return out, nil
}

func (s MySQLMemberStore) Save(data MemberData) error {
	normalizeMemberData(&data)
	if err := s.EnsureSchema(); err != nil {
		return err
	}
	db, err := s.open()
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, table := range []string{"cm_events", "cm_profile_members", "cm_profiles", "cm_profile_owners", "cm_assignments", "cm_members", "cm_settings"} {
		if _, err := tx.Exec("DELETE FROM " + table); err != nil {
			return err
		}
	}
	for _, member := range data.Members {
		if _, err := tx.Exec(`INSERT INTO cm_members (id, name, email, username, role, enabled, password_hash, password_salt, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			member.ID, member.Name, member.Email, member.Username, member.Role, member.Enabled, member.PasswordHash, member.PasswordSalt, member.CreatedAt, member.UpdatedAt); err != nil {
			return err
		}
	}
	for _, assignment := range data.Assignments {
		if _, err := tx.Exec(`INSERT INTO cm_assignments (apple_email, member_id, relation, created_at) VALUES (?, ?, ?, ?)`,
			assignment.AppleEmail, assignment.MemberID, assignment.Relation, assignment.CreatedAt); err != nil {
			return err
		}
	}
	for _, owner := range data.ProfileOwners {
		if _, err := tx.Exec(`INSERT INTO cm_profile_owners (profile_name, member_id, updated_at) VALUES (?, ?, ?)`,
			owner.ProfileName, owner.MemberID, owner.UpdatedAt); err != nil {
			return err
		}
	}
	for _, profile := range data.Profiles {
		if _, err := tx.Exec(`INSERT INTO cm_profiles (name, apple_email, enabled, profile_yaml, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
			profile.Name, profile.AppleEmail, profile.Enabled, profile.ProfileYAML, profile.CreatedAt, profile.UpdatedAt); err != nil {
			return err
		}
	}
	for _, access := range data.ProfileAccess {
		if _, err := tx.Exec(`INSERT INTO cm_profile_members (profile_name, member_id, created_at) VALUES (?, ?, ?)`,
			access.ProfileName, access.MemberID, access.CreatedAt); err != nil {
			return err
		}
	}
	settings := map[string]string{
		"auth_secret":           data.Settings.AuthSecret,
		"default_owner_email":   data.Settings.DefaultOwnerEmail,
		"default_status_filter": data.Settings.DefaultStatusFilter,
		"background_confirm":    fmt.Sprintf("%t", data.Settings.BackgroundConfirm),
		"show_released":         fmt.Sprintf("%t", data.Settings.ShowReleased),
	}
	for key, value := range settings {
		if _, err := tx.Exec(`INSERT INTO cm_settings (setting_key, setting_value) VALUES (?, ?)`, key, value); err != nil {
			return err
		}
	}
	if len(data.Events) > 500 {
		data.Events = data.Events[len(data.Events)-500:]
	}
	for _, event := range data.Events {
		if _, err := tx.Exec(`INSERT INTO cm_events (id, action, profile, apple_email, member_id, confirmed, status, message, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			event.ID, event.Action, event.Profile, event.AppleEmail, event.MemberID, event.Confirmed, event.Status, event.Message, event.CreatedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s MySQLMemberStore) ListMembers() ([]MemberWithAssignments, error) {
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

func (s MySQLMemberStore) AddMember(name, email, role string) (Member, error) {
	return addMemberToStore(s, name, email, role)
}

func (s MySQLMemberStore) AddMemberWithPassword(name, email, role, password string) (Member, error) {
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

func (s MySQLMemberStore) SetupAdmin(name, email, password string) (Member, error) {
	return setupAdminInStore(s, name, email, password)
}

func (s MySQLMemberStore) SetMemberPassword(emailOrUsername, password string) error {
	return setMemberPasswordInStore(s, emailOrUsername, password)
}

func (s MySQLMemberStore) UpdateMemberEmail(memberID, newEmail string) (Member, error) {
	return updateMemberEmailInStore(s, memberID, newEmail)
}

func (s MySQLMemberStore) VerifyMemberPassword(emailOrUsername, password string) (Member, bool, error) {
	return verifyMemberPasswordInStore(s, emailOrUsername, password)
}

func (s MySQLMemberStore) HasPasswordMembers() (bool, error) {
	return hasPasswordMembersInStore(s)
}

func (s MySQLMemberStore) SetMemberEnabled(email string, enabled bool) (Member, error) {
	return setMemberEnabledInStore(s, email, enabled)
}

func (s MySQLMemberStore) AssignMember(appleEmail, memberEmail, relation string) (AppleAccountMember, error) {
	return assignMemberInStore(s, appleEmail, memberEmail, relation)
}

func (s MySQLMemberStore) UnassignMember(appleEmail, memberEmail string) error {
	return unassignMemberInStore(s, appleEmail, memberEmail)
}

func (s MySQLMemberStore) MembersForApple(appleEmail string) ([]PublicMember, error) {
	return membersForAppleInStore(s, appleEmail)
}

func (s MySQLMemberStore) ProfileOwners() ([]PublicProfileOwner, error) {
	return profileOwnersInStore(s)
}

func (s MySQLMemberStore) ProfileOwner(profileName string) (PublicProfileOwner, bool, error) {
	return profileOwnerInStore(s, profileName)
}

func (s MySQLMemberStore) SetProfileOwner(profileName, memberEmail string) (PublicProfileOwner, error) {
	return setProfileOwnerInStore(s, profileName, memberEmail)
}

func (s MySQLMemberStore) ListManagedProfiles(memberEmail string) ([]ManagedProfile, error) {
	return listManagedProfilesInStore(s, memberEmail)
}

func (s MySQLMemberStore) UpsertManagedProfile(profile Profile) (ManagedProfile, error) {
	return upsertManagedProfileInStore(s, profile)
}

func (s MySQLMemberStore) SetManagedProfileEnabled(profileName string, enabled bool) (ManagedProfile, error) {
	return setManagedProfileEnabledInStore(s, profileName, enabled)
}

func (s MySQLMemberStore) DeleteManagedProfile(profileName string) error {
	return deleteManagedProfileInStore(s, profileName)
}

func (s MySQLMemberStore) AssignProfileAccess(profileName, memberEmail string) (ProfileAccess, error) {
	return assignProfileAccessInStore(s, profileName, memberEmail)
}

func (s MySQLMemberStore) UnassignProfileAccess(profileName, memberEmail string) error {
	return unassignProfileAccessInStore(s, profileName, memberEmail)
}

func (s MySQLMemberStore) SetMemberProfileAccess(memberEmail string, profileNames []string) ([]ProfileAccess, error) {
	return setMemberProfileAccessInStore(s, memberEmail, profileNames)
}

func (s MySQLMemberStore) MembersForProfile(profileName string) ([]PublicMember, error) {
	return membersForProfileInStore(s, profileName)
}

func (s MySQLMemberStore) WebSettings() (WebSettings, error) {
	return webSettingsInStore(s)
}

func (s MySQLMemberStore) UpdateWebSettings(update WebSettings) (WebSettings, error) {
	return updateWebSettingsInStore(s, update)
}

func (s MySQLMemberStore) EnsureAuthSecret() (string, error) {
	return ensureAuthSecretInStore(s)
}

func (s MySQLMemberStore) RecordEvent(event OperationEvent) error {
	return recordEventInStore(s, event)
}

func (s MySQLMemberStore) RecentEvents(appleEmail string, limit int) ([]OperationEvent, error) {
	return recentEventsInStore(s, appleEmail, limit)
}

func (s MySQLMemberStore) currentTime() time.Time {
	return s.normalize().Now()
}
