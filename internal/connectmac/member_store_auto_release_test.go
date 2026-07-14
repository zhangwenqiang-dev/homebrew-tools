package connectmac

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestMemberStoreCompleteAutoReleaseAtomicallyClearsMatchingOwner(t *testing.T) {
	store := NewMemberStore(filepath.Join(t.TempDir(), "members.json"))
	if _, err := store.AddMember("Owner", "owner@example.com", "operator"); err != nil {
		t.Fatalf("add owner: %v", err)
	}
	if _, err := store.SetProfileOwner("mac", "owner@example.com"); err != nil {
		t.Fatalf("set owner: %v", err)
	}
	reminder := runningAutoReleaseReminder("owner@example.com")
	if _, err := store.UpsertReleaseReminder(reminder); err != nil {
		t.Fatalf("upsert reminder: %v", err)
	}

	completed, err := store.CompleteAutoRelease(releaseReminderCycle(reminder), "2026-07-13T09:00:00Z")
	if err != nil {
		t.Fatalf("complete automatic release: %v", err)
	}
	if completed.Status != ReleaseReminderStatusReleased || completed.AutoReleaseState != ReleaseReminderAutoReleaseStateReleased {
		t.Fatalf("completed reminder = %+v", completed)
	}
	if _, ok, err := store.ProfileOwner("mac"); err != nil || ok {
		t.Fatalf("owner after completion ok=%t err=%v", ok, err)
	}
}

func TestMemberStoreCompleteAutoReleaseNeverClearsNewCycleOwner(t *testing.T) {
	store := NewMemberStore(filepath.Join(t.TempDir(), "members.json"))
	for _, member := range []struct{ name, email string }{{"Old", "old@example.com"}, {"New", "new@example.com"}} {
		if _, err := store.AddMember(member.name, member.email, "operator"); err != nil {
			t.Fatalf("add member: %v", err)
		}
	}
	old := runningAutoReleaseReminder("old@example.com")
	if _, err := store.SetProfileOwner("mac", old.OwnerEmail); err != nil {
		t.Fatalf("set old owner: %v", err)
	}
	if _, err := store.UpsertReleaseReminder(old); err != nil {
		t.Fatalf("upsert old reminder: %v", err)
	}
	newCycle := old
	newCycle.HostID = "h-new"
	newCycle.AppleEmail = "new-apple@example.com"
	newCycle.OwnerEmail = "new@example.com"
	newCycle.AutoReleaseAt = "2026-07-14T08:10:00Z"
	newCycle.AutoReleaseStartedAt = "2026-07-14T08:10:00Z"
	newCycle.Status = ReleaseReminderStatusActive
	newCycle.AutoReleaseState = ""
	if _, err := store.SetProfileOwner("mac", newCycle.OwnerEmail); err != nil {
		t.Fatalf("set new owner: %v", err)
	}
	if _, err := store.UpsertReleaseReminder(newCycle); err != nil {
		t.Fatalf("upsert new reminder: %v", err)
	}

	if _, err := store.CompleteAutoRelease(releaseReminderCycle(old), "2026-07-13T09:00:00Z"); !errors.Is(err, ErrReleaseReminderCycleChanged) {
		t.Fatalf("completion error = %v", err)
	}
	owner, ok, err := store.ProfileOwner("mac")
	if err != nil || !ok || owner.Owner.Email != newCycle.OwnerEmail {
		t.Fatalf("new owner was changed: owner=%+v ok=%t err=%v", owner, ok, err)
	}
	got, _, err := store.ReleaseReminder("mac")
	if err != nil || got.HostID != newCycle.HostID || got.Status == ReleaseReminderStatusReleased {
		t.Fatalf("new reminder was changed: reminder=%+v err=%v", got, err)
	}
}

func TestMemberStoreCompleteAutoReleaseRaceWithNewOpenIsSafe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "members.json")
	seed := NewMemberStore(path)
	for _, member := range []struct{ name, email string }{{"Old", "old@example.com"}, {"New", "new@example.com"}} {
		if _, err := seed.AddMember(member.name, member.email, "operator"); err != nil {
			t.Fatalf("add member: %v", err)
		}
	}
	old := runningAutoReleaseReminder("old@example.com")
	if _, err := seed.SetProfileOwner("mac", old.OwnerEmail); err != nil {
		t.Fatalf("set owner: %v", err)
	}
	if _, err := seed.UpsertReleaseReminder(old); err != nil {
		t.Fatalf("upsert old: %v", err)
	}
	newCycle := old
	newCycle.HostID = "h-new"
	newCycle.OwnerEmail = "new@example.com"
	newCycle.AutoReleaseAt = "2026-07-14T08:10:00Z"
	newCycle.AutoReleaseStartedAt = "2026-07-14T08:10:00Z"
	newCycle.Status = ReleaseReminderStatusActive
	newCycle.AutoReleaseState = ""

	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		store := NewMemberStore(path)
		_, _ = store.CompleteAutoRelease(releaseReminderCycle(old), "2026-07-13T09:00:00Z")
	}()
	go func() {
		defer wg.Done()
		<-start
		store := NewMemberStore(path)
		_, _ = store.SetProfileOwner("mac", newCycle.OwnerEmail)
		_, _ = store.UpsertReleaseReminder(newCycle)
	}()
	close(start)
	wg.Wait()

	owner, ok, err := seed.ProfileOwner("mac")
	if err != nil || !ok || owner.Owner.Email != newCycle.OwnerEmail {
		t.Fatalf("new owner missing after race: owner=%+v ok=%t err=%v", owner, ok, err)
	}
	reminder, _, err := seed.ReleaseReminder("mac")
	if err != nil || reminder.HostID != newCycle.HostID || reminder.Status == ReleaseReminderStatusReleased {
		t.Fatalf("new cycle lost after race: reminder=%+v err=%v", reminder, err)
	}
}

func TestMemberStoreUpsertReleaseReminderResetsAutoReleaseForNewCycle(t *testing.T) {
	for _, test := range []struct {
		name        string
		state       string
		changeApple bool
	}{
		{name: "enabled-new-host", state: ""},
		{name: "running-new-host", state: ReleaseReminderAutoReleaseStateRunning},
		{name: "retrying-new-host", state: ReleaseReminderAutoReleaseStateRetrying},
		{name: "enabled-new-apple", state: "", changeApple: true},
		{name: "running-new-apple", state: ReleaseReminderAutoReleaseStateRunning, changeApple: true},
		{name: "retrying-new-apple", state: ReleaseReminderAutoReleaseStateRetrying, changeApple: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := NewMemberStore(filepath.Join(t.TempDir(), "members.json"))
			old := runningAutoReleaseReminder("owner@example.com")
			old.AutoReleaseState = test.state
			if test.state == "" {
				old.AutoReleaseAt = "2026-07-13T08:10:00Z"
				old.AutoReleaseStartedAt = ""
				old.AutoReleaseLastAttemptAt = ""
				old.AutoReleaseAttempts = 0
			}
			if _, err := store.UpsertReleaseReminder(old); err != nil {
				t.Fatalf("upsert old reminder: %v", err)
			}

			updated := old
			if !test.changeApple {
				updated.HostID = "h-new"
			}
			if test.changeApple {
				updated.AppleEmail = "new-apple@example.com"
			}
			updated.OwnerEmail = "new-owner@example.com"
			updated.Status = ReleaseReminderStatusActive
			updated.AutoReleaseAt = "2026-07-14T08:10:00Z"
			updated.AutoReleaseStartedAt = "stale-start"
			updated.AutoReleaseLastAttemptAt = "stale-attempt"
			updated.AutoReleaseAttempts = 99
			updated.AutoReleaseLastError = "stale error"
			updated.AutoReleaseState = ReleaseReminderAutoReleaseStateRetrying
			got, err := store.UpsertReleaseReminder(updated)
			if err != nil {
				t.Fatalf("upsert new reminder: %v", err)
			}
			if got.HostID != updated.HostID || got.AppleEmail != updated.AppleEmail || got.OwnerEmail != updated.OwnerEmail || got.Status != ReleaseReminderStatusActive {
				t.Fatalf("new cycle fields not retained: %+v", got)
			}
			if got.AutoReleaseEnabled || got.AutoReleaseAt != "" || got.AutoReleaseStartedAt != "" || got.AutoReleaseLastAttemptAt != "" || got.AutoReleaseAttempts != 0 || got.AutoReleaseLastError != "" || got.AutoReleaseState != "" {
				t.Fatalf("auto-release state leaked into new cycle: %+v", got)
			}
		})
	}
}

func TestMemberStoreUpsertReleaseReminderPreservesAutoReleaseForSameCycle(t *testing.T) {
	store := NewMemberStore(filepath.Join(t.TempDir(), "members.json"))
	old := runningAutoReleaseReminder("owner@example.com")
	old.AutoReleaseState = ReleaseReminderAutoReleaseStateRetrying
	old.AutoReleaseLastError = "host is pending"
	if _, err := store.UpsertReleaseReminder(old); err != nil {
		t.Fatalf("upsert old reminder: %v", err)
	}

	updated := old
	updated.OwnerEmail = "new-owner@example.com"
	updated.Status = ReleaseReminderStatusActive
	updated.HostCreatedAt = "2026-07-13T08:00:00Z"
	got, err := store.UpsertReleaseReminder(updated)
	if err != nil {
		t.Fatalf("upsert same-cycle reminder: %v", err)
	}
	if got.HostID != old.HostID || got.AppleEmail != old.AppleEmail || got.OwnerEmail != updated.OwnerEmail || got.Status != updated.Status {
		t.Fatalf("same-cycle fields not updated: %+v", got)
	}
	if !got.AutoReleaseEnabled || got.AutoReleaseAt != old.AutoReleaseAt || got.AutoReleaseStartedAt != old.AutoReleaseStartedAt || got.AutoReleaseLastAttemptAt != old.AutoReleaseLastAttemptAt || got.AutoReleaseAttempts != old.AutoReleaseAttempts || got.AutoReleaseLastError != old.AutoReleaseLastError || got.AutoReleaseState != old.AutoReleaseState {
		t.Fatalf("same-cycle auto-release state was not preserved: %+v", got)
	}
}

func runningAutoReleaseReminder(ownerEmail string) ReleaseReminder {
	return ReleaseReminder{
		ProfileName:              "mac",
		AppleEmail:               "apple@example.com",
		HostID:                   "h-old",
		OwnerEmail:               ownerEmail,
		ReleaseDueAt:             "2026-07-13T08:00:00Z",
		Status:                   ReleaseReminderStatusDueNotified,
		AutoReleaseEnabled:       true,
		AutoReleaseAt:            "2026-07-13T08:10:00Z",
		AutoReleaseStartedAt:     "2026-07-13T08:10:00Z",
		AutoReleaseLastAttemptAt: "2026-07-13T08:10:00Z",
		AutoReleaseAttempts:      1,
		AutoReleaseState:         ReleaseReminderAutoReleaseStateRunning,
	}
}

func releaseReminderCycle(reminder ReleaseReminder) ReleaseReminderCycle {
	return ReleaseReminderCycle{
		ProfileName:          reminder.ProfileName,
		AutoReleaseAt:        reminder.AutoReleaseAt,
		AutoReleaseStartedAt: reminder.AutoReleaseStartedAt,
		HostID:               reminder.HostID,
		AppleEmail:           reminder.AppleEmail,
		OwnerEmail:           reminder.OwnerEmail,
	}
}

func TestMySQLCompleteAutoReleaseTransactionClearsOnlyMatchingOwner(t *testing.T) {
	reminder := runningAutoReleaseReminder("owner@example.com")
	tx := &fakeMySQLReleaseReminderTransaction{
		row:      fakeMySQLReleaseReminderRow{reminder: reminder},
		ownerRow: fakeMySQLProfileOwnerRow{memberID: "member-1", email: "owner@example.com"},
	}
	got, err := completeAutoReleaseInMySQLTransaction(tx, releaseReminderCycle(reminder), time.Date(2026, 7, 13, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("complete transaction: %v", err)
	}
	if got.Status != ReleaseReminderStatusReleased || !tx.ownerDeleted || !tx.committed {
		t.Fatalf("result=%+v ownerDeleted=%t committed=%t", got, tx.ownerDeleted, tx.committed)
	}
}

func TestMySQLCompleteAutoReleaseTransactionRejectsNewOwner(t *testing.T) {
	reminder := runningAutoReleaseReminder("old@example.com")
	tx := &fakeMySQLReleaseReminderTransaction{
		row:      fakeMySQLReleaseReminderRow{reminder: reminder},
		ownerRow: fakeMySQLProfileOwnerRow{memberID: "member-new", email: "new@example.com"},
	}
	if _, err := completeAutoReleaseInMySQLTransaction(tx, releaseReminderCycle(reminder), time.Now()); !errors.Is(err, ErrReleaseReminderCycleChanged) {
		t.Fatalf("error = %v", err)
	}
	if tx.ownerDeleted || tx.committed || !tx.rolledBack {
		t.Fatalf("ownerDeleted=%t committed=%t rolledBack=%t", tx.ownerDeleted, tx.committed, tx.rolledBack)
	}
}

type fakeMySQLProfileOwnerRow struct {
	memberID string
	email    string
	err      error
}

func (r fakeMySQLProfileOwnerRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*dest[0].(*string) = r.memberID
	*dest[1].(*string) = r.email
	return nil
}
