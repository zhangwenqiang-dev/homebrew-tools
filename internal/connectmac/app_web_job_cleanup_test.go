package connectmac

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWebBackgroundAWSJobTempConfigLifecycle(t *testing.T) {
	t.Run("create failure", func(t *testing.T) {
		app, config, tempDir := newWebBackgroundJobTestApp(t)
		if err := app.JobManager.BeginDrain(); err != nil {
			t.Fatalf("begin drain: %v", err)
		}
		resp := runWebBackgroundAWSJob(t, &app, config, "open")
		if !strings.Contains(resp.Body.String(), ErrJobsDraining.Error()) {
			t.Fatalf("response = %s", resp.Body.String())
		}
		assertNoWebTempConfigs(t, tempDir)
	})

	t.Run("runner startup failure", func(t *testing.T) {
		app, config, tempDir := newWebBackgroundJobTestApp(t)
		app.JobManager.Executable = filepath.Join(t.TempDir(), "missing-cm")
		resp := runWebBackgroundAWSJob(t, &app, config, "destroy")
		if !strings.Contains(resp.Body.String(), "no such file or directory") {
			t.Fatalf("response = %s", resp.Body.String())
		}
		assertNoWebTempConfigs(t, tempDir)
	})

	for _, command := range []string{"open", "destroy"} {
		for _, terminal := range []string{"success", "failure"} {
			t.Run(command+" "+terminal, func(t *testing.T) {
				app, config, tempDir := newWebBackgroundJobTestApp(t)
				resp := runWebBackgroundAWSJob(t, &app, config, command)
				if !strings.Contains(resp.Body.String(), "Started background AWS "+command+" job") {
					t.Fatalf("response = %s", resp.Body.String())
				}
				job := onlyWebBackgroundJob(t, app.JobManager)
				if job.LifecycleState != JobLifecyclePending {
					t.Fatalf("lifecycle state = %q", job.LifecycleState)
				}
				if command == "open" {
					if job.LifecycleOwnerEmail != "admin@example.com" {
						t.Fatalf("lifecycle owner email = %q", job.LifecycleOwnerEmail)
					}
				} else if job.LifecycleOwnerEmail != "" {
					t.Fatalf("destroy lifecycle owner email = %q", job.LifecycleOwnerEmail)
				}
				if len(job.CleanupPaths) != 1 {
					t.Fatalf("cleanup paths = %#v", job.CleanupPaths)
				}
				configPath := job.CleanupPaths[0]
				if _, err := os.Stat(configPath); err != nil {
					t.Fatalf("temp config unavailable before child run: %v", err)
				}
				job.Status = JobStatusRunning
				job.RunnerToken = "web-temp-token"
				job.Command = []string{"/bin/sh", "-c", `test -f "$1" || exit 9; test "$2" = success`, "sh", configPath, terminal}
				if err := app.JobManager.Save(job); err != nil {
					t.Fatalf("save runnable job: %v", err)
				}
				t.Setenv(jobRunnerTokenEnv, "web-temp-token")
				completed, err := app.JobManager.RunJob(context.Background(), job.ID)
				if terminal == "success" && err != nil {
					t.Fatalf("run successful job: %v", err)
				}
				if terminal == "failure" && err == nil {
					t.Fatal("failed job error = nil")
				}
				expectedStatus := JobStatusSuccess
				if terminal == "failure" {
					expectedStatus = JobStatusFailed
				}
				if completed.Status != expectedStatus {
					t.Fatalf("status = %s", completed.Status)
				}
				assertNoWebTempConfigs(t, tempDir)
			})
		}
	}

	t.Run("stale reconciliation", func(t *testing.T) {
		app, config, tempDir := newWebBackgroundJobTestApp(t)
		resp := runWebBackgroundAWSJob(t, &app, config, "destroy")
		if !strings.Contains(resp.Body.String(), "Started background AWS destroy job") {
			t.Fatalf("response = %s", resp.Body.String())
		}
		job := onlyWebBackgroundJob(t, app.JobManager)
		if len(job.CleanupPaths) != 1 {
			t.Fatalf("cleanup paths = %#v", job.CleanupPaths)
		}
		app.JobManager.IsRunning = func(int) bool { return false }
		if _, err := app.JobManager.Reconcile(); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		assertNoWebTempConfigs(t, tempDir)
	})
}

func newWebBackgroundJobTestApp(t *testing.T) (App, string, string) {
	t.Helper()
	dir := t.TempDir()
	tempDir := filepath.Join(dir, "tmp")
	if err := os.MkdirAll(tempDir, 0o700); err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	t.Setenv("TMPDIR", tempDir)
	key := writeSSHKey(t, 0o600)
	config := writeConfig(t, dir, key)
	var out, errOut bytes.Buffer
	app := testApp(&out, &errOut, dir)
	return app, config, tempDir
}

func runWebBackgroundAWSJob(t *testing.T, app *App, config, command string) *httptest.ResponseRecorder {
	t.Helper()
	body := `{"profile":"xcode-vnc","confirm":true,"background":true,"notify":true,"owner_email":"admin@example.com"}`
	req := httptest.NewRequest(http.MethodPost, "/api/aws/"+command, strings.NewReader(body))
	addWebAuth(t, app, req, "admin")
	if command == "destroy" {
		if _, err := app.MemberStore.SetProfileOwner("xcode-vnc", "admin@example.com"); err != nil {
			t.Fatalf("set profile owner: %v", err)
		}
	}
	rec := httptest.NewRecorder()
	app.newWebHandler(config).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	return rec
}

func onlyWebBackgroundJob(t *testing.T, manager JobManager) Job {
	t.Helper()
	jobs, err := manager.listRaw()
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("jobs = %+v", jobs)
	}
	return jobs[0]
}

func assertNoWebTempConfigs(t *testing.T, tempDir string) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(tempDir, "cm-web-config-*.yaml"))
	if err != nil {
		t.Fatalf("glob temp configs: %v", err)
	}
	if len(paths) != 0 {
		t.Fatalf("web temp configs remain: %v", paths)
	}
}
