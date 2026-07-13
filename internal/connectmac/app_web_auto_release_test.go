package connectmac

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAppWebAutoReleaseToggleAdminEnableDisable(t *testing.T) {
	for _, test := range []struct {
		name    string
		enabled bool
		seed    ReleaseReminder
	}{
		{
			name:    "enable does not schedule",
			enabled: true,
			seed: ReleaseReminder{
				ProfileName: "xcode-vnc", AppleEmail: "user@example.com", Status: ReleaseReminderStatusActive,
			},
		},
		{
			name:    "disable cancels scheduled cycle",
			enabled: false,
			seed: ReleaseReminder{
				ProfileName: "xcode-vnc", AppleEmail: "user@example.com", Status: ReleaseReminderStatusDueNotified,
				AutoReleaseEnabled: true, AutoReleaseAt: "2026-07-01T12:40:45Z",
				AutoReleaseStartedAt: "2026-07-01T12:30:45Z", AutoReleaseLastAttemptAt: "2026-07-01T12:35:45Z",
				AutoReleaseAttempts: 2, AutoReleaseLastError: "temporary", AutoReleaseState: ReleaseReminderAutoReleaseStateRetrying,
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			app := newWebAutoReleaseTestApp(t)
			if _, err := app.MemberStore.UpsertReleaseReminder(test.seed); err != nil {
				t.Fatalf("upsert reminder: %v", err)
			}

			rec := postWebAutoRelease(t, &app, "admin", test.seed.ProfileName, test.enabled)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			reminder := mustReleaseReminder(t, app, test.seed.ProfileName)
			if reminder.AutoReleaseEnabled != test.enabled {
				t.Fatalf("enabled = %t, want %t", reminder.AutoReleaseEnabled, test.enabled)
			}
			if reminder.AutoReleaseAt != "" || reminder.AutoReleaseStartedAt != "" || reminder.AutoReleaseLastAttemptAt != "" || reminder.AutoReleaseAttempts != 0 || reminder.AutoReleaseLastError != "" || reminder.AutoReleaseState != "" {
				t.Fatalf("automatic release cycle was not clear: %+v", reminder)
			}
			if !strings.Contains(rec.Body.String(), `"auto_release_enabled":`+map[bool]string{true: "true", false: "false"}[test.enabled]) {
				t.Fatalf("response does not contain updated reminder: %s", rec.Body.String())
			}

			events, err := app.MemberStore.RecentEvents(test.seed.AppleEmail, 10)
			if err != nil {
				t.Fatalf("recent events: %v", err)
			}
			wantState := "disabled"
			if test.enabled {
				wantState = "enabled"
			}
			if len(events) != 1 || events[0].Action != "release-reminder.auto-release" || events[0].Profile != test.seed.ProfileName || events[0].AppleEmail != test.seed.AppleEmail || events[0].MemberID == "" || events[0].Status != "success" || !strings.Contains(events[0].Message, "admin@example.com") || !strings.Contains(events[0].Message, wantState) {
				t.Fatalf("events = %+v", events)
			}
		})
	}
}

func TestAppWebAutoReleaseToggleRoleAndValidation(t *testing.T) {
	for _, role := range []string{"operator", "viewer"} {
		t.Run(role+" forbidden", func(t *testing.T) {
			app := newWebAutoReleaseTestApp(t)
			seedWebAutoReleaseReminder(t, app, ReleaseReminderStatusActive)
			rec := postWebAutoRelease(t, &app, role, "xcode-vnc", true)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
		})
	}

	t.Run("profile required", func(t *testing.T) {
		app := newWebAutoReleaseTestApp(t)
		rec := postWebAutoRelease(t, &app, "admin", " ", true)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "profile is required") {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("reminder missing", func(t *testing.T) {
		app := newWebAutoReleaseTestApp(t)
		rec := postWebAutoRelease(t, &app, "admin", "missing", true)
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "release reminder not found") {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
	})
}

func TestAppWebAutoReleaseEnablePreservesRunningCycleWithActiveDestroyJob(t *testing.T) {
	app := newWebAutoReleaseTestApp(t)
	seed := ReleaseReminder{
		ProfileName: "xcode-vnc", AppleEmail: "user@example.com", Status: ReleaseReminderStatusDueNotified,
		AutoReleaseEnabled: false, AutoReleaseAt: "2026-07-01T12:40:45Z",
		AutoReleaseStartedAt: "2026-07-01T12:30:45Z", AutoReleaseLastAttemptAt: "2026-07-01T12:35:45Z",
		AutoReleaseAttempts: 3, AutoReleaseLastError: "destroy in progress", AutoReleaseState: ReleaseReminderAutoReleaseStateRunning,
	}
	saved, err := app.MemberStore.UpsertReleaseReminder(seed)
	if err != nil {
		t.Fatalf("upsert reminder: %v", err)
	}
	if _, err := app.JobManager.Create(Job{ID: "active-destroy", Type: "aws-destroy", Profile: seed.ProfileName, Status: JobStatusRunning, PID: 42}); err != nil {
		t.Fatalf("create active job: %v", err)
	}

	rec := postWebAutoRelease(t, &app, "admin", seed.ProfileName, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	got := mustReleaseReminder(t, app, seed.ProfileName)
	if !got.AutoReleaseEnabled {
		t.Fatal("auto release was not enabled")
	}
	got.AutoReleaseEnabled = saved.AutoReleaseEnabled
	got.UpdatedAt = saved.UpdatedAt
	if !reflect.DeepEqual(got, saved) {
		t.Fatalf("enable changed fields beyond flag:\n got: %+v\nwant: %+v", got, saved)
	}
}

func TestAppWebAutoReleaseToggleRejectsRunningRelease(t *testing.T) {
	for _, test := range []struct {
		name      string
		seed      ReleaseReminder
		activeJob bool
	}{
		{
			name: "running state",
			seed: ReleaseReminder{ProfileName: "xcode-vnc", AutoReleaseEnabled: true, AutoReleaseAt: "2026-07-01T12:40:45Z", AutoReleaseAttempts: 1, AutoReleaseState: ReleaseReminderAutoReleaseStateRunning},
		},
		{
			name:      "active destroy job",
			seed:      ReleaseReminder{ProfileName: "xcode-vnc", AutoReleaseEnabled: true, AutoReleaseAt: "2026-07-01T12:40:45Z", AutoReleaseState: ReleaseReminderAutoReleaseStateScheduled},
			activeJob: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			app := newWebAutoReleaseTestApp(t)
			if _, err := app.MemberStore.UpsertReleaseReminder(test.seed); err != nil {
				t.Fatalf("upsert reminder: %v", err)
			}
			if test.activeJob {
				if _, err := app.JobManager.Create(Job{ID: "active-destroy", Type: "aws-destroy", Profile: "xcode-vnc", Status: JobStatusRunning, PID: 42}); err != nil {
					t.Fatalf("create active job: %v", err)
				}
			}

			rec := postWebAutoRelease(t, &app, "admin", "xcode-vnc", false)
			if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "automatic release") {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			got := mustReleaseReminder(t, app, "xcode-vnc")
			if !got.AutoReleaseEnabled || got.AutoReleaseAt != test.seed.AutoReleaseAt || got.AutoReleaseState != test.seed.AutoReleaseState || got.AutoReleaseAttempts != test.seed.AutoReleaseAttempts {
				t.Fatalf("running release was modified: %+v", got)
			}
		})
	}
}

func TestAppWebReleaseReminderExtendBoundaryAndCycleReset(t *testing.T) {
	serverNow := time.Date(2026, 7, 1, 12, 30, 45, 0, time.UTC)
	for _, test := range []struct {
		name       string
		dueAt      time.Time
		wantStatus int
	}{
		{name: "less than ten minutes", dueAt: serverNow.Add(AutoReleaseGracePeriod - time.Second), wantStatus: http.StatusBadRequest},
		{name: "exactly ten minutes", dueAt: serverNow.Add(AutoReleaseGracePeriod), wantStatus: http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			app := newWebAutoReleaseTestApp(t)
			seedWebAutoReleaseReminder(t, app, ReleaseReminderStatusDueNotified)
			rec := postWebExtension(t, &app, "admin", test.dueAt)
			if rec.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d, body = %s", rec.Code, test.wantStatus, rec.Body.String())
			}
		})
	}

	app := newWebAutoReleaseTestApp(t)
	seedWebAutoReleaseReminder(t, app, ReleaseReminderStatusDueNotified)
	dueAt := serverNow.Add(time.Hour)
	rec := postWebExtension(t, &app, "admin", dueAt)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	got := mustReleaseReminder(t, app, "xcode-vnc")
	if got.ReleaseDueAt != dueAt.Format(time.RFC3339) || got.LastExtendedAt != serverNow.Format(time.RFC3339) || got.Status != ReleaseReminderStatusActive || got.LastNotifiedAt != "" || !got.AutoReleaseEnabled || got.AutoReleaseAt != "" || got.AutoReleaseStartedAt != "" || got.AutoReleaseLastAttemptAt != "" || got.AutoReleaseAttempts != 0 || got.AutoReleaseLastError != "" || got.AutoReleaseState != "" {
		t.Fatalf("extended reminder = %+v", got)
	}
}

func TestAppWebReleaseReminderExtendRejectsRunningStateOrJob(t *testing.T) {
	for _, test := range []struct {
		name      string
		state     string
		activeJob bool
	}{
		{name: "running state", state: ReleaseReminderAutoReleaseStateRunning},
		{name: "active destroy job", state: ReleaseReminderAutoReleaseStateScheduled, activeJob: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			app := newWebAutoReleaseTestApp(t)
			reminder := seedWebAutoReleaseReminder(t, app, ReleaseReminderStatusDueNotified)
			var err error
			reminder, err = app.MemberStore.UpdateReleaseReminder(reminder.ProfileName, func(current ReleaseReminder) (ReleaseReminder, error) {
				current.AutoReleaseState = test.state
				return current, nil
			})
			if err != nil {
				t.Fatalf("update reminder: %v", err)
			}
			if test.activeJob {
				if _, err := app.JobManager.Create(Job{ID: "active-destroy", Type: "aws-destroy", Profile: "xcode-vnc", Status: JobStatusRunning, PID: 42}); err != nil {
					t.Fatalf("create active job: %v", err)
				}
			}
			rec := postWebExtension(t, &app, "admin", app.JobManager.Now().Add(time.Hour))
			if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "automatic release") {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			got := mustReleaseReminder(t, app, "xcode-vnc")
			if got.ReleaseDueAt != reminder.ReleaseDueAt || got.AutoReleaseState != reminder.AutoReleaseState || got.AutoReleaseAt != reminder.AutoReleaseAt {
				t.Fatalf("running release was modified: %+v", got)
			}
		})
	}
}

func TestAppWebReleaseReminderExtensionWinsAtomicRaceWithAutoClaim(t *testing.T) {
	app := newWebAutoReleaseTestApp(t)
	now := app.JobManager.Now()
	reminder := seedWebAutoReleaseReminder(t, app, ReleaseReminderStatusDueNotified)
	reminder.ReleaseDueAt = now.Add(-AutoReleaseGracePeriod).Format(time.RFC3339)
	reminder.AutoReleaseAt = now.Format(time.RFC3339)
	reminder.AutoReleaseState = ReleaseReminderAutoReleaseStateScheduled
	if _, err := app.MemberStore.UpsertReleaseReminder(reminder); err != nil {
		t.Fatalf("update reminder: %v", err)
	}

	gate := &firstUpdateGateRepository{
		MemberRepository: app.MemberStore,
		entered:          make(chan struct{}),
		release:          make(chan struct{}),
	}
	app.MemberStore = gate
	unexpectedAWS := errors.New("auto claim reached AWS after extension won")
	coordinator := AutoReleaseCoordinator{
		Now:   func() time.Time { return now },
		Store: gate,
		Jobs:  app.JobManager,
		ResolveProfile: func(context.Context, ReleaseReminder) (Profile, error) {
			return Profile{}, unexpectedAWS
		},
		Status: func(context.Context, Profile) (AWSStatus, error) {
			return AWSStatus{}, unexpectedAWS
		},
		StartDestroy: func(context.Context, Profile) (Job, error) {
			return Job{}, unexpectedAWS
		},
	}

	response := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		response <- postWebExtension(t, &app, "admin", now.Add(time.Hour))
	}()
	<-gate.entered
	claimDone := make(chan error, 1)
	go func() { claimDone <- coordinator.Scan(context.Background()) }()
	close(gate.release)

	rec := <-response
	if rec.Code != http.StatusOK {
		t.Fatalf("extension status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if err := <-claimDone; err != nil {
		t.Fatalf("coordinator scan: %v", err)
	}
	got := mustReleaseReminder(t, app, "xcode-vnc")
	if got.Status != ReleaseReminderStatusActive || got.AutoReleaseState != "" {
		t.Fatalf("unsafe race result: reminder=%+v", got)
	}
}

func TestAppWebReleaseReminderAutoClaimWinsBeforeExtension(t *testing.T) {
	app := newWebAutoReleaseTestApp(t)
	now := app.JobManager.Now()
	reminder := seedWebAutoReleaseReminder(t, app, ReleaseReminderStatusDueNotified)
	var err error
	reminder, err = app.MemberStore.UpdateReleaseReminder(reminder.ProfileName, func(current ReleaseReminder) (ReleaseReminder, error) {
		current.ReleaseDueAt = now.Add(-AutoReleaseGracePeriod).Format(time.RFC3339)
		current.AutoReleaseAt = now.Format(time.RFC3339)
		current.AutoReleaseState = ReleaseReminderAutoReleaseStateScheduled
		return current, nil
	})
	if err != nil {
		t.Fatalf("update reminder: %v", err)
	}

	coordinator := AutoReleaseCoordinator{Store: app.MemberStore}
	claimed, err := coordinator.claim(reminder, now)
	if err != nil {
		t.Fatalf("claim reminder: %v", err)
	}
	if claimed.AutoReleaseState != ReleaseReminderAutoReleaseStateRunning {
		t.Fatalf("claim state = %q", claimed.AutoReleaseState)
	}

	rec := postWebExtension(t, &app, "admin", now.Add(time.Hour))
	if rec.Code != http.StatusConflict {
		t.Fatalf("extension status = %d, body = %s", rec.Code, rec.Body.String())
	}
	got := mustReleaseReminder(t, app, reminder.ProfileName)
	if !reflect.DeepEqual(got, claimed) {
		t.Fatalf("extension modified claimed reminder:\n got: %+v\nwant: %+v", got, claimed)
	}
}

type firstUpdateGateRepository struct {
	MemberRepository
	mu      sync.Mutex
	gated   bool
	entered chan struct{}
	release chan struct{}
}

func (r *firstUpdateGateRepository) UpdateReleaseReminder(profileName string, update func(ReleaseReminder) (ReleaseReminder, error)) (ReleaseReminder, error) {
	r.mu.Lock()
	gate := !r.gated
	if gate {
		r.gated = true
	}
	r.mu.Unlock()
	if !gate {
		return r.MemberRepository.UpdateReleaseReminder(profileName, update)
	}
	return r.MemberRepository.UpdateReleaseReminder(profileName, func(reminder ReleaseReminder) (ReleaseReminder, error) {
		close(r.entered)
		<-r.release
		return update(reminder)
	})
}

func newWebAutoReleaseTestApp(t *testing.T) App {
	t.Helper()
	var out, errOut bytes.Buffer
	return testApp(&out, &errOut, t.TempDir())
}

func seedWebAutoReleaseReminder(t *testing.T, app App, status string) ReleaseReminder {
	t.Helper()
	reminder := ReleaseReminder{
		ProfileName: "xcode-vnc", AppleEmail: "user@example.com", HostID: "h-1",
		ReleaseDueAt: "2026-07-01T12:20:45Z", OwnerEmail: "admin@example.com", OwnerName: "Test Admin",
		LastNotifiedAt: "2026-07-01T12:30:45Z", Status: status, AutoReleaseEnabled: true,
		AutoReleaseAt: "2026-07-01T12:40:45Z", AutoReleaseStartedAt: "2026-07-01T12:30:45Z",
		AutoReleaseLastAttemptAt: "2026-07-01T12:35:45Z", AutoReleaseAttempts: 2,
		AutoReleaseLastError: "temporary", AutoReleaseState: ReleaseReminderAutoReleaseStateRetrying,
	}
	if _, err := app.MemberStore.UpsertReleaseReminder(reminder); err != nil {
		t.Fatalf("upsert reminder: %v", err)
	}
	return reminder
}

func postWebAutoRelease(t *testing.T, app *App, role, profile string, enabled bool) *httptest.ResponseRecorder {
	t.Helper()
	body := strings.NewReader(`{"profile":"` + profile + `","enabled":` + map[bool]string{true: "true", false: "false"}[enabled] + `}`)
	req := httptest.NewRequest(http.MethodPost, "/api/release-reminder/auto-release", body)
	addWebAuth(t, app, req, role)
	rec := httptest.NewRecorder()
	app.newWebHandler("").ServeHTTP(rec, req)
	return rec
}

func postWebExtension(t *testing.T, app *App, role string, dueAt time.Time) *httptest.ResponseRecorder {
	t.Helper()
	body := strings.NewReader(`{"profile":"xcode-vnc","release_due_at":"` + dueAt.UTC().Format(time.RFC3339) + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/release-reminder/extend", body)
	addWebAuth(t, app, req, role)
	rec := httptest.NewRecorder()
	app.newWebHandler("").ServeHTTP(rec, req)
	return rec
}

func mustReleaseReminder(t *testing.T, app App, profile string) ReleaseReminder {
	t.Helper()
	reminder, ok, err := app.MemberStore.ReleaseReminder(profile)
	if err != nil || !ok {
		t.Fatalf("release reminder %q: ok=%t err=%v", profile, ok, err)
	}
	return reminder
}
