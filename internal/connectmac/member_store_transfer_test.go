package connectmac

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMemberStoreTransferCreateListUpdateDelete(t *testing.T) {
	store := NewMemberStore(filepath.Join(t.TempDir(), "members.json"))
	now := time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC)
	store.Now = func() time.Time { return now }

	created, err := store.CreateTransferRecord("member-a", TransferRecord{
		MemberID:    "member-a",
		MemberEmail: "a@example.com",
		ProfileName: "iossupport-usw2",
		Direction:   TransferDirectionPush,
		LocalPath:   "/tmp/App",
		RemotePath:  "~/Documents/",
		Status:      TransferStatusCreated,
	})
	if err != nil {
		t.Fatalf("create transfer: %v", err)
	}
	if !strings.HasPrefix(created.ID, "transfer-") || created.CreatedAt != now.Format(time.RFC3339) || created.UpdatedAt != created.CreatedAt {
		t.Fatalf("created transfer = %+v", created)
	}

	second, err := store.CreateTransferRecord("member-a", TransferRecord{
		MemberID:    "member-a",
		MemberEmail: "a@example.com",
		ProfileName: "other-profile",
		Direction:   TransferDirectionPull,
		LocalPath:   "/tmp/Download",
		RemotePath:  "~/Downloads/",
		Status:      TransferStatusQueued,
	})
	if err != nil {
		t.Fatalf("create second transfer: %v", err)
	}
	if second.ID == created.ID {
		t.Fatalf("transfer IDs must be unique: %q", created.ID)
	}

	records, err := store.ListTransferRecords("member-a", "iossupport-usw2")
	if err != nil {
		t.Fatalf("list transfers: %v", err)
	}
	if len(records) != 1 || records[0].ID != created.ID {
		t.Fatalf("records = %+v", records)
	}

	now = now.Add(time.Minute)
	updated, err := store.UpdateTransferRecord("member-a", created.ID, "local-job-1", func(current TransferRecord) (TransferRecord, error) {
		current.LocalJobID = "local-job-1"
		current.Status = TransferStatusRunning
		current.Percent = 25
		current.StartedAt = now.Format(time.RFC3339)
		return current, nil
	})
	if err != nil {
		t.Fatalf("update transfer: %v", err)
	}
	if updated.Status != TransferStatusRunning || updated.Percent != 25 || updated.UpdatedAt != now.Format(time.RFC3339) {
		t.Fatalf("updated transfer = %+v", updated)
	}

	if err := store.DeleteTransferRecord("member-a", created.ID); err != nil {
		t.Fatalf("delete transfer: %v", err)
	}
	records, err = store.ListTransferRecords("member-a", "")
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	if len(records) != 1 || records[0].ID != second.ID {
		t.Fatalf("records after delete = %+v", records)
	}
}

func TestMemberStoreTransferRejectsPercentRegressionAndTerminalReactivation(t *testing.T) {
	store := NewMemberStore(filepath.Join(t.TempDir(), "members.json"))
	record := mustCreateTransferRecord(t, store, "member-a", "a@example.com")

	record, err := store.UpdateTransferRecord("member-a", record.ID, "job-a", func(current TransferRecord) (TransferRecord, error) {
		current.LocalJobID = "job-a"
		current.Status = TransferStatusRunning
		current.Percent = 75
		return current, nil
	})
	if err != nil {
		t.Fatalf("set running transfer: %v", err)
	}
	if _, err := store.UpdateTransferRecord("member-a", record.ID, "job-a", func(current TransferRecord) (TransferRecord, error) {
		current.Percent = 50
		return current, nil
	}); err == nil {
		t.Fatal("expected percent regression to fail")
	}

	record, err = store.UpdateTransferRecord("member-a", record.ID, "job-a", func(current TransferRecord) (TransferRecord, error) {
		current.Status = TransferStatusFailed
		current.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		return current, nil
	})
	if err != nil {
		t.Fatalf("finish transfer: %v", err)
	}
	if _, err := store.UpdateTransferRecord("member-a", record.ID, "job-a", func(current TransferRecord) (TransferRecord, error) {
		current.Status = TransferStatusInterrupted
		current.Percent = 99
		current.ErrorSummary = "changed after terminal"
		return current, nil
	}); err == nil {
		t.Fatal("expected terminal transfer mutation to fail")
	}
}

func TestMemberStoreTransferSurvivesStaleWholeStoreSave(t *testing.T) {
	store := NewMemberStore(filepath.Join(t.TempDir(), "members.json"))
	stale, err := store.Load()
	if err != nil {
		t.Fatalf("load stale snapshot: %v", err)
	}
	created := mustCreateTransferRecord(t, store, "member-a", "a@example.com")

	stale.Settings.DefaultStatusFilter = "ready"
	if err := store.Save(stale); err != nil {
		t.Fatalf("save stale snapshot: %v", err)
	}

	records, err := store.ListTransferRecords("member-a", "")
	if err != nil {
		t.Fatalf("list transfers: %v", err)
	}
	if len(records) != 1 || records[0].ID != created.ID {
		t.Fatalf("transfer record lost after stale save: %+v", records)
	}
}

func TestMemberStoreTransferTwoMemberIsolation(t *testing.T) {
	store := NewMemberStore(filepath.Join(t.TempDir(), "members.json"))
	first := mustCreateTransferRecord(t, store, "member-a", "a@example.com")
	second := mustCreateTransferRecord(t, store, "member-b", "b@example.com")

	records, err := store.ListTransferRecords("member-a", "")
	if err != nil {
		t.Fatalf("list member-a: %v", err)
	}
	if len(records) != 1 || records[0].ID != first.ID {
		t.Fatalf("member-a records = %+v", records)
	}
	if _, err := store.UpdateTransferRecord("member-b", first.ID, "", func(current TransferRecord) (TransferRecord, error) {
		current.Status = TransferStatusRunning
		return current, nil
	}); err == nil {
		t.Fatal("expected cross-member update to fail")
	}
	if err := store.DeleteTransferRecord("member-b", first.ID); err == nil {
		t.Fatal("expected cross-member delete to fail")
	}
	if _, err := store.UpdateTransferRecord("member-a", first.ID, "wrong-job", func(current TransferRecord) (TransferRecord, error) {
		current.LocalJobID = "different-job"
		return current, nil
	}); err == nil {
		t.Fatal("expected local job ID mismatch to fail")
	}
	if err := store.DeleteTransferRecord("member-b", second.ID); err != nil {
		t.Fatalf("delete member-b transfer: %v", err)
	}
}

func mustCreateTransferRecord(t *testing.T, store MemberStore, memberID, memberEmail string) TransferRecord {
	t.Helper()
	record, err := store.CreateTransferRecord(memberID, TransferRecord{
		MemberID:    memberID,
		MemberEmail: memberEmail,
		ProfileName: "iossupport-usw2",
		Direction:   TransferDirectionPush,
		LocalPath:   "/tmp/App",
		RemotePath:  "~/Documents/",
		Status:      TransferStatusCreated,
	})
	if err != nil {
		t.Fatalf("create transfer for %s: %v", memberID, err)
	}
	return record
}
