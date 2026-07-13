package connectmac

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const DefaultJobDir = "~/.connectmac/jobs"

type JobStatus string

const (
	JobStatusRunning     JobStatus = "running"
	JobStatusSuccess     JobStatus = "success"
	JobStatusFailed      JobStatus = "failed"
	JobStatusDeferred    JobStatus = "deferred"
	JobStatusInterrupted JobStatus = "interrupted"
	JobStatusUnknown     JobStatus = "unknown"
)

const interruptedJobError = "background process exited before recording completion"

const jobRunnerTokenEnv = "CM_JOB_RUNNER_TOKEN"

type Job struct {
	ID          string    `json:"id"`
	Type        string    `json:"type"`
	Profile     string    `json:"profile"`
	AppleEmail  string    `json:"apple_email,omitempty"`
	Status      JobStatus `json:"status"`
	PID         int       `json:"pid,omitempty"`
	StartedAt   time.Time `json:"started_at"`
	FinishedAt  time.Time `json:"finished_at,omitempty"`
	Log         string    `json:"log"`
	Command     []string  `json:"command"`
	Notify      bool      `json:"notify"`
	ExitCode    *int      `json:"exit_code,omitempty"`
	LastError   string    `json:"last_error,omitempty"`
	CompletedBy int       `json:"completed_by,omitempty"`
	RunnerToken string    `json:"runner_token,omitempty"`
}

type JobsDrainingError struct{}

func (*JobsDrainingError) Error() string { return "background jobs are draining" }

var ErrJobsDraining = &JobsDrainingError{}

type JobManager struct {
	Dir        string
	Executable string
	Now        func() time.Time
	IsRunning  func(pid int) bool
	Sleep      func(ctx context.Context, duration time.Duration) error
	Rename     func(oldPath, newPath string) error
	Notify     func(title, message string) error
}

type WaitAllTimeoutError struct {
	Timeout time.Duration
	Active  []Job
}

func (e *WaitAllTimeoutError) Error() string {
	return fmt.Sprintf("timed out after %s waiting for %d active job(s)", e.Timeout, len(e.Active))
}

func NewJobManager(dir string) JobManager {
	return JobManager{
		Dir:       dir,
		Now:       time.Now,
		IsRunning: ProcessRunning,
		Sleep:     sleepContext,
		Rename:    os.Rename,
		Notify:    SendMacNotification,
	}
}

func (m JobManager) normalize() JobManager {
	if m.Dir == "" {
		m.Dir = DefaultJobDir
	}
	if m.Now == nil {
		m.Now = time.Now
	}
	if m.IsRunning == nil {
		m.IsRunning = ProcessRunning
	}
	if m.Sleep == nil {
		m.Sleep = sleepContext
	}
	if m.Rename == nil {
		m.Rename = os.Rename
	}
	if m.Notify == nil {
		m.Notify = SendMacNotification
	}
	return m
}

func (m JobManager) Create(job Job) (Job, error) {
	m = m.normalize()
	if job.StartedAt.IsZero() {
		job.StartedAt = m.Now()
	}
	if job.Status == "" {
		job.Status = JobStatusRunning
	}
	if job.ID == "" {
		job.ID = newJobID(job.Type, job.Profile, job.StartedAt)
	}
	var created Job
	err := m.withGlobalLock(func(baseDir string) error {
		if _, err := os.Stat(filepath.Join(baseDir, ".draining")); err == nil {
			return ErrJobsDraining
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("check jobs drain marker: %w", err)
		}
		path, err := m.JobPath(job.ID)
		if err != nil {
			return err
		}
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create job dir %s: %w", dir, err)
		}
		if job.Log == "" {
			job.Log = filepath.Join(dir, "run.log")
		}
		return m.withJobLock(job.ID, func() error {
			if err := m.Save(job); err != nil {
				return err
			}
			created = job
			return nil
		})
	})
	if err != nil {
		return Job{}, err
	}
	return created, nil
}

func (m JobManager) Save(job Job) error {
	m = m.normalize()
	path, err := m.JobPath(job.ID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create job dir %s: %w", filepath.Dir(path), err)
	}
	data, err := json.MarshalIndent(job, "", "  ")
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".job-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary job file: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return fmt.Errorf("chmod temporary job file: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return fmt.Errorf("write temporary job file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary job file: %w", err)
	}
	if err := m.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace job file: %w", err)
	}
	return nil
}

func (m JobManager) Load(id string) (Job, error) {
	if _, err := m.loadRaw(id); err != nil {
		return Job{}, err
	}
	if _, err := m.reconcileID(id); err != nil {
		return Job{}, err
	}
	return m.loadRaw(id)
}

func (m JobManager) loadRaw(id string) (Job, error) {
	path, err := m.JobPath(id)
	if err != nil {
		return Job{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Job{}, err
	}
	var job Job
	if err := json.Unmarshal(data, &job); err != nil {
		return Job{}, err
	}
	if job.ID != id {
		return Job{}, fmt.Errorf("job ID mismatch: requested %q, file contains %q", id, job.ID)
	}
	return job, nil
}

func (m JobManager) List() ([]Job, error) {
	if _, err := m.Reconcile(); err != nil {
		return nil, err
	}
	return m.listRaw()
}

func (m JobManager) listRaw() ([]Job, error) {
	m = m.normalize()
	dir, err := ExpandPath(m.Dir)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	jobs := []Job{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		job, err := m.loadRaw(entry.Name())
		if err != nil {
			return nil, fmt.Errorf("load job %s: %w", entry.Name(), err)
		}
		jobs = append(jobs, job)
	}
	sort.Slice(jobs, func(i, j int) bool {
		return jobs[i].StartedAt.After(jobs[j].StartedAt)
	})
	return jobs, nil
}

func (m JobManager) Reconcile() ([]Job, error) {
	m = m.normalize()
	jobs, err := m.listRaw()
	if err != nil {
		return nil, err
	}
	changed := make([]Job, 0)
	for _, job := range jobs {
		updated, err := m.reconcileID(job.ID)
		if err != nil {
			return nil, err
		}
		if updated != nil {
			changed = append(changed, *updated)
		}
	}
	return changed, nil
}

func (m JobManager) reconcileID(id string) (*Job, error) {
	m = m.normalize()
	var changed *Job
	err := m.withJobLock(id, func() error {
		job, err := m.loadRaw(id)
		if err != nil {
			return err
		}
		if job.Status != JobStatusRunning || job.PID <= 0 || m.IsRunning(job.PID) {
			return nil
		}
		job.Status = JobStatusInterrupted
		job.FinishedAt = m.Now()
		job.LastError = interruptedJobError
		if err := m.Save(job); err != nil {
			return fmt.Errorf("save reconciled job %s: %w", id, err)
		}
		changed = &job
		return nil
	})
	return changed, err
}

func (m JobManager) Active() ([]Job, error) {
	m = m.normalize()
	if _, err := m.Reconcile(); err != nil {
		return nil, err
	}
	jobs, err := m.listRaw()
	if err != nil {
		return nil, err
	}
	active := make([]Job, 0)
	for _, job := range jobs {
		if job.Status == JobStatusRunning {
			active = append(active, job)
		}
	}
	return active, nil
}

func (m JobManager) WaitAll(ctx context.Context, timeout, interval time.Duration, progress func(time.Duration, []Job)) error {
	m = m.normalize()
	if timeout <= 0 {
		return fmt.Errorf("timeout must be positive")
	}
	if interval <= 0 {
		return fmt.Errorf("interval must be positive")
	}
	started := m.Now()
	slept := false
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		active, err := m.Active()
		if err != nil {
			return err
		}
		if len(active) == 0 {
			return nil
		}
		elapsed := m.Now().Sub(started)
		if slept && progress != nil {
			progress(elapsed, active)
		}
		if elapsed >= timeout {
			return &WaitAllTimeoutError{Timeout: timeout, Active: active}
		}
		wait := interval
		if remaining := timeout - elapsed; wait > remaining {
			wait = remaining
		}
		if err := m.Sleep(ctx, wait); err != nil {
			return err
		}
		slept = true
	}
}

func (m JobManager) BeginDrain() error {
	m = m.normalize()
	return m.withGlobalLock(func(baseDir string) error {
		path := filepath.Join(baseDir, ".draining")
		file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return fmt.Errorf("create jobs drain marker: %w", err)
		}
		if err := file.Chmod(0o600); err != nil {
			file.Close()
			return fmt.Errorf("chmod jobs drain marker: %w", err)
		}
		if err := file.Close(); err != nil {
			return fmt.Errorf("close jobs drain marker: %w", err)
		}
		return nil
	})
}

func (m JobManager) EndDrain() error {
	m = m.normalize()
	return m.withGlobalLock(func(baseDir string) error {
		if err := os.Remove(filepath.Join(baseDir, ".draining")); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove jobs drain marker: %w", err)
		}
		return nil
	})
}

func (m JobManager) withGlobalLock(fn func(baseDir string) error) error {
	m = m.normalize()
	baseDir, err := ExpandPath(m.Dir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(baseDir, 0o700); err != nil {
		return fmt.Errorf("create jobs dir %s: %w", baseDir, err)
	}
	return withFileLock(filepath.Join(baseDir, ".lock"), func() error { return fn(baseDir) })
}

func (m JobManager) withJobLock(id string, fn func() error) error {
	path, err := m.JobPath(id)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create job dir %s: %w", dir, err)
	}
	return withFileLock(filepath.Join(dir, ".lock"), fn)
}

func withFileLock(path string, fn func() error) (err error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open lock file %s: %w", path, err)
	}
	defer file.Close()
	if err := file.Chmod(0o600); err != nil {
		return fmt.Errorf("chmod lock file %s: %w", path, err)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX); err != nil {
		return fmt.Errorf("lock %s: %w", path, err)
	}
	defer func() {
		if unlockErr := unix.Flock(int(file.Fd()), unix.LOCK_UN); err == nil && unlockErr != nil {
			err = fmt.Errorf("unlock %s: %w", path, unlockErr)
		}
	}()
	return fn()
}

func (m JobManager) StartRunner(ctx context.Context, job Job) (Job, error) {
	m = m.normalize()
	executable := m.Executable
	if executable == "" {
		var err error
		executable, err = os.Executable()
		if err != nil {
			return m.failRunnerStartup(job.ID, err)
		}
	}
	logFile, err := os.OpenFile(job.Log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return m.failRunnerStartup(job.ID, err)
	}
	defer logFile.Close()
	token, err := newRunnerToken()
	if err != nil {
		return m.failRunnerStartup(job.ID, err)
	}
	cmd := exec.CommandContext(ctx, executable, "job", "run", job.ID)
	cmd.Env = append(environmentWithout(jobRunnerTokenEnv), jobRunnerTokenEnv+"="+token)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	var started Job
	err = m.withJobLock(job.ID, func() error {
		current, err := m.loadRaw(job.ID)
		if err != nil {
			return err
		}
		if current.Status != JobStatusRunning || current.PID != 0 || current.CompletedBy != 0 {
			return fmt.Errorf("job %s is not eligible to start", job.ID)
		}
		current.RunnerToken = token
		if err := m.Save(current); err != nil {
			return err
		}
		if err := cmd.Start(); err != nil {
			current.Status = JobStatusFailed
			current.FinishedAt = m.Now()
			current.LastError = err.Error()
			current.RunnerToken = ""
			if saveErr := m.Save(current); saveErr != nil {
				return errors.Join(err, saveErr)
			}
			started = current
			return err
		}
		current.PID = cmd.Process.Pid
		if err := m.Save(current); err != nil {
			current.Status = JobStatusFailed
			current.FinishedAt = m.Now()
			current.LastError = err.Error()
			current.RunnerToken = ""
			_ = m.Save(current)
			return err
		}
		if err := cmd.Process.Release(); err != nil {
			current.Status = JobStatusFailed
			current.FinishedAt = m.Now()
			current.LastError = err.Error()
			current.RunnerToken = ""
			if saveErr := m.Save(current); saveErr != nil {
				return errors.Join(err, saveErr)
			}
			return err
		}
		started = current
		return nil
	})
	if err != nil {
		if started.ID != "" {
			return started, err
		}
		return m.failRunnerStartup(job.ID, err)
	}
	return started, nil
}

func (m JobManager) RunJob(ctx context.Context, id string) (Job, error) {
	m = m.normalize()
	token := os.Getenv(jobRunnerTokenEnv)
	if token == "" {
		return Job{}, errors.New("job run is restricted to the internal background runner")
	}
	var job Job
	err := m.withJobLock(id, func() error {
		current, err := m.loadRaw(id)
		if err != nil {
			return err
		}
		if current.Status != JobStatusRunning {
			return fmt.Errorf("job %s cannot run with status %s", id, current.Status)
		}
		if current.RunnerToken == "" || current.RunnerToken != token {
			return errors.New("invalid or already consumed background runner token")
		}
		if current.PID != 0 && current.CompletedBy != 0 {
			return fmt.Errorf("job %s has already been claimed", id)
		}
		current.RunnerToken = ""
		current.CompletedBy = os.Getpid()
		if err := m.Save(current); err != nil {
			return err
		}
		job = current
		return nil
	})
	if err != nil {
		return Job{}, err
	}
	if len(job.Command) == 0 {
		return m.finishRunJob(id, JobStatusFailed, nil, errors.New("job command is empty"))
	}
	logFile, err := os.OpenFile(job.Log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return m.finishRunJob(id, JobStatusFailed, nil, err)
	}
	defer logFile.Close()
	fmt.Fprintf(logFile, "cm job %s started at %s\n", job.ID, m.Now().Format(time.RFC3339))
	cmd := exec.CommandContext(ctx, job.Command[0], job.Command[1:]...)
	cmd.Env = environmentWithout(jobRunnerTokenEnv)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	err = cmd.Run()
	exitCode := 0
	if err != nil {
		exitCode = 1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
		job.LastError = err.Error()
	}
	status := inferJobStatus(exitCode, readJobLog(job.Log))
	job, finishErr := m.finishRunJob(id, status, &exitCode, err)
	if finishErr != nil {
		err = finishErr
	}
	fmt.Fprintf(logFile, "cm job %s finished with status %s at %s\n", job.ID, job.Status, job.FinishedAt.Format(time.RFC3339))
	if job.Notify {
		message := fmt.Sprintf("%s %s", job.Type, job.Status)
		if job.Profile != "" {
			message = fmt.Sprintf("%s: %s", job.Profile, message)
		}
		_ = m.Notify("ConnectMac", message)
	}
	return job, err
}

func (m JobManager) finishRunJob(id string, status JobStatus, exitCode *int, runErr error) (Job, error) {
	var finished Job
	err := m.withJobLock(id, func() error {
		job, err := m.loadRaw(id)
		if err != nil {
			return err
		}
		if job.Status != JobStatusRunning {
			finished = job
			return fmt.Errorf("job %s changed to terminal status %s before runner completion", id, job.Status)
		}
		job.Status = status
		job.ExitCode = exitCode
		job.FinishedAt = m.Now()
		if runErr != nil {
			job.LastError = runErr.Error()
		}
		if err := m.Save(job); err != nil {
			return err
		}
		finished = job
		return nil
	})
	if err != nil {
		return finished, errors.Join(runErr, err)
	}
	return finished, runErr
}

func (m JobManager) failRunnerStartup(id string, cause error) (Job, error) {
	var failed Job
	saveErr := m.withJobLock(id, func() error {
		job, err := m.loadRaw(id)
		if err != nil {
			return err
		}
		if job.Status == JobStatusRunning && job.PID == 0 {
			job.Status = JobStatusFailed
			job.FinishedAt = m.Now()
			job.LastError = cause.Error()
			job.RunnerToken = ""
			if err := m.Save(job); err != nil {
				return err
			}
		}
		failed = job
		return nil
	})
	return failed, errors.Join(cause, saveErr)
}

func newRunnerToken() (string, error) {
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf("generate background runner token: %w", err)
	}
	return hex.EncodeToString(data), nil
}

func environmentWithout(key string) []string {
	prefix := key + "="
	environment := make([]string, 0, len(os.Environ()))
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, prefix) {
			environment = append(environment, value)
		}
	}
	return environment
}

func (m JobManager) JobPath(id string) (string, error) {
	m = m.normalize()
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\`) || strings.Contains(id, string(os.PathSeparator)) {
		return "", fmt.Errorf("invalid job id %q", id)
	}
	dir, err := ExpandPath(m.Dir)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, id, "job.json"), nil
}

func newJobID(jobType, profile string, at time.Time) string {
	parts := []string{slugPart(jobType), slugPart(profile), at.Format("20060102150405")}
	out := []string{}
	for _, part := range parts {
		if part != "" {
			out = append(out, part)
		}
	}
	if len(out) == 0 {
		return "job-" + at.Format("20060102150405")
	}
	return strings.Join(out, "-")
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func slugPart(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func inferJobStatus(exitCode int, logText string) JobStatus {
	if exitCode != 0 {
		return JobStatusFailed
	}
	lower := strings.ToLower(logText)
	if strings.Contains(lower, "need rerun: true") || strings.Contains(lower, "deferred") {
		return JobStatusDeferred
	}
	return JobStatusSuccess
}

func readJobLog(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func formatJobsTable(jobs []Job) string {
	rows := [][]string{{"ID", "TYPE", "PROFILE", "STATUS", "PID", "STARTED"}}
	for _, job := range jobs {
		pid := ""
		if job.PID > 0 {
			pid = strconv.Itoa(job.PID)
		}
		started := ""
		if !job.StartedAt.IsZero() {
			started = job.StartedAt.Format("2006-01-02 15:04:05")
		}
		rows = append(rows, []string{job.ID, job.Type, job.Profile, string(job.Status), pid, started})
	}
	return formatRows(rows)
}

func SendMacNotification(title, message string) error {
	script := fmt.Sprintf("display notification %s with title %s", appleScriptString(message), appleScriptString(title))
	return exec.Command("osascript", "-e", script).Run()
}

func appleScriptString(value string) string {
	escaped := strings.ReplaceAll(value, "\\", "\\\\")
	escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
	return "\"" + escaped + "\""
}

func copyTail(out io.Writer, path string, maxBytes int64) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if maxBytes > 0 && info.Size() > maxBytes {
		if _, err := file.Seek(info.Size()-maxBytes, io.SeekStart); err != nil {
			return err
		}
		fmt.Fprintln(out, "... truncated ...")
	}
	_, err = io.Copy(out, file)
	return err
}
