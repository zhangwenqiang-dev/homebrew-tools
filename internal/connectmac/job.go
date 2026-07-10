package connectmac

import (
	"context"
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
}

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
	path, err := m.JobPath(job.ID)
	if err != nil {
		return Job{}, err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Job{}, fmt.Errorf("create job dir %s: %w", dir, err)
	}
	if job.Log == "" {
		job.Log = filepath.Join(dir, "run.log")
	}
	if err := m.Save(job); err != nil {
		return Job{}, err
	}
	return job, nil
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
	return job, nil
}

func (m JobManager) List() ([]Job, error) {
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
		job, err := m.Load(entry.Name())
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

func (m JobManager) Reconcile() error {
	m = m.normalize()
	jobs, err := m.List()
	if err != nil {
		return err
	}
	for _, job := range jobs {
		if job.Status != JobStatusRunning || job.PID <= 0 || m.IsRunning(job.PID) {
			continue
		}
		job.Status = JobStatusInterrupted
		job.FinishedAt = m.Now()
		job.LastError = interruptedJobError
		if err := m.Save(job); err != nil {
			return fmt.Errorf("save reconciled job %s: %w", job.ID, err)
		}
	}
	return nil
}

func (m JobManager) Active() ([]Job, error) {
	m = m.normalize()
	if err := m.Reconcile(); err != nil {
		return nil, err
	}
	jobs, err := m.List()
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
		if elapsed > 0 && progress != nil {
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
	}
}

func (m JobManager) StartRunner(ctx context.Context, job Job) (Job, error) {
	m = m.normalize()
	executable := m.Executable
	if executable == "" {
		var err error
		executable, err = os.Executable()
		if err != nil {
			return Job{}, err
		}
	}
	cmd := exec.CommandContext(ctx, executable, "job", "run", job.ID)
	logFile, err := os.OpenFile(job.Log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return Job{}, err
	}
	defer logFile.Close()
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		return Job{}, err
	}
	job.PID = cmd.Process.Pid
	if err := cmd.Process.Release(); err != nil {
		return Job{}, err
	}
	if err := m.Save(job); err != nil {
		return Job{}, err
	}
	return job, nil
}

func (m JobManager) RunJob(ctx context.Context, id string) (Job, error) {
	m = m.normalize()
	job, err := m.Load(id)
	if err != nil {
		return Job{}, err
	}
	if len(job.Command) == 0 {
		job.Status = JobStatusFailed
		job.LastError = "job command is empty"
		finished := m.Now()
		job.FinishedAt = finished
		_ = m.Save(job)
		return job, errors.New(job.LastError)
	}
	logFile, err := os.OpenFile(job.Log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return Job{}, err
	}
	defer logFile.Close()
	fmt.Fprintf(logFile, "cm job %s started at %s\n", job.ID, m.Now().Format(time.RFC3339))
	cmd := exec.CommandContext(ctx, job.Command[0], job.Command[1:]...)
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
	job.ExitCode = &exitCode
	job.CompletedBy = os.Getpid()
	job.FinishedAt = m.Now()
	job.Status = inferJobStatus(exitCode, readJobLog(job.Log))
	fmt.Fprintf(logFile, "cm job %s finished with status %s at %s\n", job.ID, job.Status, job.FinishedAt.Format(time.RFC3339))
	if saveErr := m.Save(job); saveErr != nil && err == nil {
		err = saveErr
	}
	if job.Notify {
		message := fmt.Sprintf("%s %s", job.Type, job.Status)
		if job.Profile != "" {
			message = fmt.Sprintf("%s: %s", job.Profile, message)
		}
		_ = m.Notify("ConnectMac", message)
	}
	return job, err
}

func (m JobManager) JobPath(id string) (string, error) {
	m = m.normalize()
	if id == "" || strings.Contains(id, "/") || strings.Contains(id, string(os.PathSeparator)) {
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
