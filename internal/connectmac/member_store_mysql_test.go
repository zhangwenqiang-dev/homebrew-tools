package connectmac

import (
	"strings"
	"testing"
)

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
