package connectmac

import (
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

type fakeMySQLReleaseReminderRow struct {
	reminder ReleaseReminder
	err      error
}

func (r fakeMySQLReleaseReminderRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != 22 {
		return errors.New("unexpected release reminder scan destination count")
	}
	*dest[0].(*string) = r.reminder.ProfileName
	*dest[1].(*string) = r.reminder.AppleEmail
	*dest[2].(*string) = r.reminder.HostID
	*dest[3].(*string) = r.reminder.HostCreatedAt
	*dest[4].(*string) = r.reminder.ReleaseDueAt
	*dest[5].(*string) = r.reminder.OwnerEmail
	*dest[6].(*string) = r.reminder.OwnerName
	*dest[7].(*string) = r.reminder.LastExtendedByEmail
	*dest[8].(*string) = r.reminder.LastExtendedByName
	*dest[9].(*string) = r.reminder.LastExtendedAt
	*dest[10].(*string) = r.reminder.LastNotifiedAt
	*dest[11].(*string) = r.reminder.ReleasedAt
	*dest[12].(*string) = r.reminder.Status
	*dest[13].(*bool) = r.reminder.AutoReleaseEnabled
	*dest[14].(*string) = r.reminder.AutoReleaseAt
	*dest[15].(*string) = r.reminder.AutoReleaseStartedAt
	*dest[16].(*string) = r.reminder.AutoReleaseLastAttemptAt
	*dest[17].(*int) = r.reminder.AutoReleaseAttempts
	*dest[18].(*string) = r.reminder.AutoReleaseLastError
	*dest[19].(*string) = r.reminder.AutoReleaseState
	*dest[20].(*string) = r.reminder.CreatedAt
	*dest[21].(*string) = r.reminder.UpdatedAt
	return nil
}

type fakeMySQLReleaseReminderTransaction struct {
	row         mysqlReleaseReminderScanner
	query       string
	queryArgs   []any
	execQuery   string
	execArgs    []any
	written     ReleaseReminder
	execErr     error
	commitErr   error
	rollbackErr error
	committed   bool
	rolledBack  bool
}

func (tx *fakeMySQLReleaseReminderTransaction) QueryRow(query string, args ...any) mysqlReleaseReminderScanner {
	tx.query = query
	tx.queryArgs = args
	return tx.row
}

func (tx *fakeMySQLReleaseReminderTransaction) Exec(query string, args ...any) error {
	tx.execQuery = query
	tx.execArgs = args
	if tx.execErr == nil && len(args) == 22 {
		tx.written = ReleaseReminder{
			ProfileName:              args[0].(string),
			AppleEmail:               args[1].(string),
			HostID:                   args[2].(string),
			HostCreatedAt:            args[3].(string),
			ReleaseDueAt:             args[4].(string),
			OwnerEmail:               args[5].(string),
			OwnerName:                args[6].(string),
			LastExtendedByEmail:      args[7].(string),
			LastExtendedByName:       args[8].(string),
			LastExtendedAt:           args[9].(string),
			LastNotifiedAt:           args[10].(string),
			ReleasedAt:               args[11].(string),
			Status:                   args[12].(string),
			AutoReleaseEnabled:       args[13].(bool),
			AutoReleaseAt:            args[14].(string),
			AutoReleaseStartedAt:     args[15].(string),
			AutoReleaseLastAttemptAt: args[16].(string),
			AutoReleaseAttempts:      args[17].(int),
			AutoReleaseLastError:     args[18].(string),
			AutoReleaseState:         args[19].(string),
			CreatedAt:                args[20].(string),
			UpdatedAt:                args[21].(string),
		}
	} else if tx.execErr == nil && len(args) == 21 {
		tx.written = ReleaseReminder{
			ProfileName:              args[20].(string),
			AppleEmail:               args[0].(string),
			HostID:                   args[1].(string),
			HostCreatedAt:            args[2].(string),
			ReleaseDueAt:             args[3].(string),
			OwnerEmail:               args[4].(string),
			OwnerName:                args[5].(string),
			LastExtendedByEmail:      args[6].(string),
			LastExtendedByName:       args[7].(string),
			LastExtendedAt:           args[8].(string),
			LastNotifiedAt:           args[9].(string),
			ReleasedAt:               args[10].(string),
			Status:                   args[11].(string),
			AutoReleaseEnabled:       args[12].(bool),
			AutoReleaseAt:            args[13].(string),
			AutoReleaseStartedAt:     args[14].(string),
			AutoReleaseLastAttemptAt: args[15].(string),
			AutoReleaseAttempts:      args[16].(int),
			AutoReleaseLastError:     args[17].(string),
			AutoReleaseState:         args[18].(string),
			UpdatedAt:                args[19].(string),
		}
		if row, ok := tx.row.(fakeMySQLReleaseReminderRow); ok {
			tx.written.CreatedAt = row.reminder.CreatedAt
		}
	}
	return tx.execErr
}

func (tx *fakeMySQLReleaseReminderTransaction) Commit() error {
	tx.committed = true
	return tx.commitErr
}

func (tx *fakeMySQLReleaseReminderTransaction) Rollback() error {
	tx.rolledBack = true
	return tx.rollbackErr
}

func TestMySQLReleaseReminderAutoReleaseSchemaMigrations(t *testing.T) {
	wantColumns := []string{
		"auto_release_enabled",
		"auto_release_at",
		"auto_release_started_at",
		"auto_release_last_attempt_at",
		"auto_release_attempts",
		"auto_release_last_error",
		"auto_release_state",
	}
	joined := strings.Join(mysqlReleaseReminderMigrationStatements, "\n")
	for _, column := range wantColumns {
		if !strings.Contains(joined, "ADD COLUMN "+column) {
			t.Errorf("release reminder migration missing %s", column)
		}
	}
	if strings.Contains(strings.ToUpper(joined), "DROP ") || strings.Contains(strings.ToUpper(joined), "DELETE ") {
		t.Fatalf("release reminder migrations are destructive: %s", joined)
	}
}

func TestMySQLReleaseReminderSelectColumnsIncludeAutoReleaseState(t *testing.T) {
	wantColumns := []string{
		"auto_release_enabled",
		"auto_release_at",
		"auto_release_started_at",
		"auto_release_last_attempt_at",
		"auto_release_attempts",
		"auto_release_last_error",
		"auto_release_state",
	}
	for _, column := range wantColumns {
		if !strings.Contains(mysqlReleaseReminderSelectColumns, column) {
			t.Errorf("release reminder SELECT columns missing %s", column)
		}
	}
	if !strings.Contains(mysqlReleaseReminderSelectForUpdate, "WHERE profile_name = ? FOR UPDATE") {
		t.Fatalf("release reminder update query does not lock one profile row: %s", mysqlReleaseReminderSelectForUpdate)
	}
}

func TestMySQLSaveReleaseReminderInsertRoundTripsThroughScan(t *testing.T) {
	want := ReleaseReminder{
		ProfileName:              "apple-usw2",
		AppleEmail:               "apple@example.com",
		HostID:                   "h-123",
		HostCreatedAt:            "2026-07-01T08:00:00Z",
		ReleaseDueAt:             "2026-07-02T08:00:00Z",
		OwnerEmail:               "owner@example.com",
		OwnerName:                "Owner",
		LastExtendedByEmail:      "admin@example.com",
		LastExtendedByName:       "Admin",
		LastExtendedAt:           "2026-07-01T09:00:00Z",
		LastNotifiedAt:           "2026-07-01T10:00:00Z",
		ReleasedAt:               "2026-07-02T09:00:00Z",
		Status:                   ReleaseReminderStatusReleased,
		AutoReleaseEnabled:       true,
		AutoReleaseAt:            "2026-07-02T08:00:00Z",
		AutoReleaseStartedAt:     "2026-07-02T08:01:00Z",
		AutoReleaseLastAttemptAt: "2026-07-02T08:02:00Z",
		AutoReleaseAttempts:      3,
		AutoReleaseLastError:     "previous failure",
		AutoReleaseState:         ReleaseReminderAutoReleaseStateReleased,
		CreatedAt:                "2026-07-01T08:00:00Z",
		UpdatedAt:                "2026-07-02T09:00:00Z",
	}
	tx := &fakeMySQLReleaseReminderTransaction{}
	if err := insertMySQLReleaseReminder(tx, want); err != nil {
		t.Fatalf("insert reminder: %v", err)
	}
	wantQuery := `INSERT INTO cm_release_reminders (profile_name, apple_email, host_id, host_created_at, release_due_at, owner_email, owner_name, last_extended_by_email, last_extended_by_name, last_extended_at, last_notified_at, released_at, status, auto_release_enabled, auto_release_at, auto_release_started_at, auto_release_last_attempt_at, auto_release_attempts, auto_release_last_error, auto_release_state, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	if tx.execQuery != wantQuery {
		t.Fatalf("INSERT query = %q, want %q", tx.execQuery, wantQuery)
	}
	if len(tx.execArgs) != 22 {
		t.Fatalf("INSERT arg count = %d, want 22", len(tx.execArgs))
	}
	var got ReleaseReminder
	if err := scanMySQLReleaseReminder(fakeMySQLReleaseReminderRow{reminder: tx.written}, &got); err != nil {
		t.Fatalf("scan inserted reminder: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("inserted reminder round trip = %+v, want %+v", got, want)
	}
}

func TestMySQLUpdateReleaseReminderSelectScanAndWriteRoundTrip(t *testing.T) {
	current := ReleaseReminder{
		ProfileName:              "apple-usw2",
		AppleEmail:               "apple@example.com",
		HostID:                   "h-123",
		HostCreatedAt:            "2026-07-01T08:00:00Z",
		ReleaseDueAt:             "2026-07-02T08:00:00Z",
		OwnerEmail:               "owner@example.com",
		OwnerName:                "Owner",
		LastExtendedByEmail:      "admin@example.com",
		LastExtendedByName:       "Admin",
		LastExtendedAt:           "2026-07-01T09:00:00Z",
		LastNotifiedAt:           "2026-07-01T10:00:00Z",
		ReleasedAt:               "",
		Status:                   ReleaseReminderStatusActive,
		AutoReleaseEnabled:       true,
		AutoReleaseAt:            "2026-07-02T08:00:00Z",
		AutoReleaseStartedAt:     "2026-07-02T08:01:00Z",
		AutoReleaseLastAttemptAt: "2026-07-02T08:02:00Z",
		AutoReleaseAttempts:      2,
		AutoReleaseLastError:     "pending",
		AutoReleaseState:         ReleaseReminderAutoReleaseStateRetrying,
		CreatedAt:                "2026-07-01T08:00:00Z",
		UpdatedAt:                "2026-07-02T08:02:00Z",
	}
	tx := &fakeMySQLReleaseReminderTransaction{row: fakeMySQLReleaseReminderRow{reminder: current}}
	now := time.Date(2026, 7, 2, 8, 3, 0, 0, time.UTC)

	got, err := updateReleaseReminderInMySQLTransaction(tx, current.ProfileName, now, func(reminder ReleaseReminder) (ReleaseReminder, error) {
		if !reflect.DeepEqual(reminder, current) {
			t.Fatalf("scanned reminder = %+v, want %+v", reminder, current)
		}
		reminder.AutoReleaseAttempts++
		reminder.AutoReleaseLastError = ""
		reminder.AutoReleaseState = ReleaseReminderAutoReleaseStateReleased
		return reminder, nil
	})
	if err != nil {
		t.Fatalf("update reminder: %v", err)
	}
	want := current
	want.AutoReleaseAttempts = 3
	want.AutoReleaseLastError = ""
	want.AutoReleaseState = ReleaseReminderAutoReleaseStateReleased
	want.UpdatedAt = now.Format(time.RFC3339)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("updated reminder = %+v, want %+v", got, want)
	}
	if tx.query != mysqlReleaseReminderSelectForUpdate || len(tx.queryArgs) != 1 || tx.queryArgs[0] != current.ProfileName {
		t.Fatalf("SELECT query = %q args=%v", tx.query, tx.queryArgs)
	}
	if !strings.HasPrefix(tx.execQuery, "UPDATE cm_release_reminders SET ") {
		t.Fatalf("UPDATE query = %q", tx.execQuery)
	}
	if len(tx.execArgs) != 21 {
		t.Fatalf("UPDATE arg count = %d, want 21", len(tx.execArgs))
	}
	if !reflect.DeepEqual(tx.written, want) {
		t.Fatalf("written reminder = %+v, want %+v", tx.written, want)
	}
	if !tx.committed || tx.rolledBack {
		t.Fatalf("transaction committed=%t rolledBack=%t", tx.committed, tx.rolledBack)
	}
}

func TestMySQLUpdateReleaseReminderMissingRollsBack(t *testing.T) {
	tx := &fakeMySQLReleaseReminderTransaction{row: fakeMySQLReleaseReminderRow{err: sql.ErrNoRows}}
	called := false
	_, err := updateReleaseReminderInMySQLTransaction(tx, "missing", time.Time{}, func(reminder ReleaseReminder) (ReleaseReminder, error) {
		called = true
		return reminder, nil
	})
	if err == nil || !strings.Contains(err.Error(), "release reminder for profile missing not found") {
		t.Fatalf("missing reminder error = %v", err)
	}
	if called || tx.committed || !tx.rolledBack || tx.execQuery != "" {
		t.Fatalf("missing transaction called=%t committed=%t rolledBack=%t exec=%q", called, tx.committed, tx.rolledBack, tx.execQuery)
	}
}

func TestMySQLUpdateReleaseReminderCallbackErrorRollsBack(t *testing.T) {
	wantErr := errors.New("stop update")
	tx := &fakeMySQLReleaseReminderTransaction{row: fakeMySQLReleaseReminderRow{reminder: ReleaseReminder{ProfileName: "apple-usw2"}}}
	_, err := updateReleaseReminderInMySQLTransaction(tx, "apple-usw2", time.Time{}, func(reminder ReleaseReminder) (ReleaseReminder, error) {
		reminder.HostID = "changed"
		return reminder, wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("callback error = %v, want %v", err, wantErr)
	}
	if tx.committed || !tx.rolledBack || tx.execQuery != "" {
		t.Fatalf("callback error committed=%t rolledBack=%t exec=%q", tx.committed, tx.rolledBack, tx.execQuery)
	}
}

func TestMySQLUpdateReleaseReminderWriteAndCommitErrorsRollBack(t *testing.T) {
	for _, test := range []struct {
		name      string
		execErr   error
		commitErr error
	}{
		{name: "write", execErr: errors.New("write failed")},
		{name: "commit", commitErr: errors.New("commit failed")},
	} {
		t.Run(test.name, func(t *testing.T) {
			tx := &fakeMySQLReleaseReminderTransaction{
				row:       fakeMySQLReleaseReminderRow{reminder: ReleaseReminder{ProfileName: "apple-usw2"}},
				execErr:   test.execErr,
				commitErr: test.commitErr,
			}
			_, err := updateReleaseReminderInMySQLTransaction(tx, "apple-usw2", time.Time{}, func(reminder ReleaseReminder) (ReleaseReminder, error) {
				return reminder, nil
			})
			wantErr := test.execErr
			if wantErr == nil {
				wantErr = test.commitErr
			}
			if !errors.Is(err, wantErr) {
				t.Fatalf("transaction error = %v, want %v", err, wantErr)
			}
			if !tx.rolledBack {
				t.Fatal("failed transaction was not rolled back")
			}
		})
	}
}
