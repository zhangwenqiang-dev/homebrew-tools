package connectmac

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMemberStoreCRUDAndEvents(t *testing.T) {
	store := NewMemberStore(filepath.Join(t.TempDir(), "members.json"))
	store.Now = func() time.Time {
		return time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)
	}

	member, err := store.AddMember("王恒辉", "WHH@example.com", "")
	if err != nil {
		t.Fatalf("add member: %v", err)
	}
	if member.Email != "whh@example.com" || member.Role != "operator" || !member.Enabled {
		t.Fatalf("member = %+v", member)
	}
	assignment, err := store.AssignMember("Apple@example.com", "whh@example.com", "")
	if err != nil {
		t.Fatalf("assign member: %v", err)
	}
	if assignment.AppleEmail != "apple@example.com" || assignment.Relation != "owner" {
		t.Fatalf("assignment = %+v", assignment)
	}
	owners, err := store.MembersForApple("APPLE@example.com")
	if err != nil {
		t.Fatalf("members for apple: %v", err)
	}
	if len(owners) != 1 || owners[0].Email != "whh@example.com" {
		t.Fatalf("owners = %+v", owners)
	}
	profileOwner, err := store.SetProfileOwner("apple-usw2", "whh@example.com")
	if err != nil {
		t.Fatalf("set profile owner: %v", err)
	}
	if profileOwner.ProfileName != "apple-usw2" || profileOwner.Owner.Email != "whh@example.com" {
		t.Fatalf("profile owner = %+v", profileOwner)
	}
	profileOwner, ok, err := store.ProfileOwner("apple-usw2")
	if err != nil {
		t.Fatalf("profile owner lookup: %v", err)
	}
	if !ok || profileOwner.Owner.Email != "whh@example.com" {
		t.Fatalf("profile owner lookup = %+v ok=%t", profileOwner, ok)
	}
	profileOwners, err := store.ProfileOwners()
	if err != nil {
		t.Fatalf("profile owners: %v", err)
	}
	if len(profileOwners) != 1 || profileOwners[0].ProfileName != "apple-usw2" {
		t.Fatalf("profile owners = %+v", profileOwners)
	}
	disabled, err := store.SetMemberEnabled("whh@example.com", false)
	if err != nil {
		t.Fatalf("disable member: %v", err)
	}
	if disabled.Enabled {
		t.Fatalf("member should be disabled: %+v", disabled)
	}
	if err := store.RecordEvent(OperationEvent{
		Action:     "open",
		Profile:    "apple-usw2",
		AppleEmail: "apple@example.com",
		Confirmed:  true,
		Status:     "success",
		Message:    "started",
	}); err != nil {
		t.Fatalf("record event: %v", err)
	}
	events, err := store.RecentEvents("apple@example.com", 10)
	if err != nil {
		t.Fatalf("recent events: %v", err)
	}
	if len(events) != 1 || events[0].Action != "open" || events[0].Status != "success" {
		t.Fatalf("events = %+v", events)
	}
	if err := store.UnassignMember("apple@example.com", "whh@example.com"); err != nil {
		t.Fatalf("unassign member: %v", err)
	}
	owners, err = store.MembersForApple("apple@example.com")
	if err != nil {
		t.Fatalf("members for apple after unassign: %v", err)
	}
	if len(owners) != 0 {
		t.Fatalf("owners after unassign = %+v", owners)
	}
}

func TestMemberStoreManagedProfilesAccess(t *testing.T) {
	store := NewMemberStore(filepath.Join(t.TempDir(), "members.json"))
	store.Now = func() time.Time {
		return time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)
	}
	if _, err := store.AddMember("Admin", "admin@example.com", "admin"); err != nil {
		t.Fatalf("add admin: %v", err)
	}
	if _, err := store.AddMember("User", "user@example.com", "operator"); err != nil {
		t.Fatalf("add user: %v", err)
	}
	profile := Profile{Name: "apple-usw2", Description: "Apple account: apple@example.com"}
	profile.AWS.AccountEmail = "apple@example.com"
	profile.AWS.Profile = "cm-xcode"
	profile.AWS.Region = "us-west-2"
	record, err := store.UpsertManagedProfile(profile)
	if err != nil {
		t.Fatalf("upsert profile: %v", err)
	}
	if record.Name != "apple-usw2" || !record.Enabled || !strings.Contains(record.ProfileYAML, "apple-usw2") {
		t.Fatalf("record = %+v", record)
	}
	memberProfiles, err := store.ListManagedProfiles("user@example.com")
	if err != nil {
		t.Fatalf("list member profiles: %v", err)
	}
	if len(memberProfiles) != 0 {
		t.Fatalf("member profiles before grant = %+v", memberProfiles)
	}
	if _, err := store.AssignProfileAccess("apple-usw2", "user@example.com"); err != nil {
		t.Fatalf("grant profile: %v", err)
	}
	memberProfiles, err = store.ListManagedProfiles("user@example.com")
	if err != nil {
		t.Fatalf("list member profiles after grant: %v", err)
	}
	if len(memberProfiles) != 1 || memberProfiles[0].Name != "apple-usw2" {
		t.Fatalf("member profiles after grant = %+v", memberProfiles)
	}
	members, err := store.ListMembers()
	if err != nil {
		t.Fatalf("list members: %v", err)
	}
	var userProfiles []string
	for _, member := range members {
		if member.Email == "user@example.com" {
			userProfiles = member.Profiles
			break
		}
	}
	if len(userProfiles) != 1 || userProfiles[0] != "apple-usw2" {
		t.Fatalf("member profile names = %+v", userProfiles)
	}
	adminProfiles, err := store.ListManagedProfiles("admin@example.com")
	if err != nil {
		t.Fatalf("list admin profiles: %v", err)
	}
	if len(adminProfiles) != 1 {
		t.Fatalf("admin profiles = %+v", adminProfiles)
	}
}
