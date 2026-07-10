package connectmac

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	LocalTransferQueued      = "queued"
	LocalTransferRunning     = "running"
	LocalTransferSucceeded   = "succeeded"
	LocalTransferFailed      = "failed"
	LocalTransferInterrupted = "interrupted"

	localTransferOutputLimit = 64 * 1024
	localTransferRetention   = 24 * time.Hour
)

var (
	ErrLocalTransferDraining = errors.New("local transfer manager is draining")
	rsyncPercentPattern      = regexp.MustCompile(`(?:^|\s)(\d{1,3})%`)
	rsyncToCheckPattern      = regexp.MustCompile(`to-(?:chk|check)=(\d+)/(\d+)`)
)

type LocalTransferJob struct {
	ID         string     `json:"id"`
	Profile    string     `json:"profile"`
	Direction  string     `json:"direction"`
	Status     string     `json:"status"`
	Percent    int        `json:"percent"`
	Output     string     `json:"output"`
	Error      string     `json:"error"`
	CreatedAt  time.Time  `json:"created_at"`
	StartedAt  *time.Time `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
}

func (j LocalTransferJob) Active() bool {
	return j.Status == LocalTransferQueued || j.Status == LocalTransferRunning
}

type LocalTransferJobManager struct {
	mu        sync.Mutex
	jobs      map[string]*LocalTransferJob
	now       func() time.Time
	retention time.Duration
	sequence  uint64
	draining  bool
}

func NewLocalTransferJobManager() *LocalTransferJobManager {
	return &LocalTransferJobManager{
		jobs:      make(map[string]*LocalTransferJob),
		now:       time.Now,
		retention: localTransferRetention,
	}
}

func (m *LocalTransferJobManager) Start(profile, direction string, run func(func(string)) error) (LocalTransferJob, error) {
	m.mu.Lock()
	m.cleanupLocked()
	if m.draining {
		m.mu.Unlock()
		return LocalTransferJob{}, ErrLocalTransferDraining
	}
	for _, job := range m.jobs {
		if job.Profile == profile && job.Direction == direction && job.Active() {
			result := *job
			m.mu.Unlock()
			return result, nil
		}
	}
	m.sequence++
	created := m.now()
	job := &LocalTransferJob{
		ID:        fmt.Sprintf("transfer-%d-%d", created.UnixNano(), m.sequence),
		Profile:   profile,
		Direction: direction,
		Status:    LocalTransferQueued,
		CreatedAt: created,
	}
	m.jobs[job.ID] = job
	result := *job
	m.mu.Unlock()

	go m.run(job.ID, run)
	return result, nil
}

func (m *LocalTransferJobManager) TryDrain() ([]LocalTransferJob, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupLocked()
	active := make([]LocalTransferJob, 0)
	for _, job := range m.jobs {
		if job.Active() {
			active = append(active, *job)
		}
	}
	if len(active) > 0 {
		sort.Slice(active, func(i, j int) bool { return active[i].CreatedAt.Before(active[j].CreatedAt) })
		return active, false
	}
	m.draining = true
	return active, true
}

func (m *LocalTransferJobManager) Resume() {
	m.mu.Lock()
	m.draining = false
	m.mu.Unlock()
}

func (m *LocalTransferJobManager) Get(id string) (LocalTransferJob, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupLocked()
	job, ok := m.jobs[id]
	if !ok {
		return LocalTransferJob{}, false
	}
	return *job, true
}

func (m *LocalTransferJobManager) List(profile string) []LocalTransferJob {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupLocked()
	jobs := make([]LocalTransferJob, 0, len(m.jobs))
	for _, job := range m.jobs {
		if profile == "" || job.Profile == profile {
			jobs = append(jobs, *job)
		}
	}
	sort.Slice(jobs, func(i, j int) bool {
		return jobs[i].CreatedAt.After(jobs[j].CreatedAt)
	})
	return jobs
}

func (m *LocalTransferJobManager) Active() []LocalTransferJob {
	jobs := m.List("")
	active := jobs[:0]
	for _, job := range jobs {
		if job.Active() {
			active = append(active, job)
		}
	}
	return active
}

func (m *LocalTransferJobManager) run(id string, run func(func(string)) error) {
	m.mu.Lock()
	job, ok := m.jobs[id]
	if !ok {
		m.mu.Unlock()
		return
	}
	started := m.now()
	job.Status = LocalTransferRunning
	job.StartedAt = &started
	m.mu.Unlock()

	err := run(func(output string) {
		m.appendOutput(id, output)
	})

	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok = m.jobs[id]
	if !ok {
		return
	}
	finished := m.now()
	job.FinishedAt = &finished
	switch {
	case err == nil:
		job.Status = LocalTransferSucceeded
		job.Percent = 100
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		job.Status = LocalTransferInterrupted
		job.Error = localTransferFailureError(job.Output, err)
	default:
		job.Status = LocalTransferFailed
		job.Error = localTransferFailureError(job.Output, err)
	}
}

func localTransferFailureError(output string, err error) string {
	detail := strings.TrimSpace(output)
	cause := ""
	if err != nil {
		cause = strings.TrimSpace(err.Error())
	}
	switch {
	case detail == "":
		return cause
	case cause == "":
		return detail
	case detail == cause, strings.Contains(detail, cause):
		return detail
	case strings.Contains(cause, detail):
		return cause
	default:
		return detail + "\n" + cause
	}
}

func (m *LocalTransferJobManager) appendOutput(id, output string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[id]
	if !ok {
		return
	}
	job.Output += output
	if len(job.Output) > localTransferOutputLimit {
		job.Output = job.Output[len(job.Output)-localTransferOutputLimit:]
	}
	if progress, ok := parseRsyncProgress(job.Output); ok && progress > job.Percent {
		job.Percent = progress
	}
}

func (m *LocalTransferJobManager) cleanupLocked() {
	cutoff := m.now().Add(-m.retention)
	for id, job := range m.jobs {
		if job.Active() || job.FinishedAt == nil {
			continue
		}
		if job.FinishedAt.Before(cutoff) {
			delete(m.jobs, id)
		}
	}
}

func parseRsyncProgress(output string) (int, bool) {
	toCheckMatches := rsyncToCheckPattern.FindAllStringSubmatch(output, -1)
	if len(toCheckMatches) > 0 {
		match := toCheckMatches[len(toCheckMatches)-1]
		remaining, errRemaining := strconv.Atoi(match[1])
		total, errTotal := strconv.Atoi(match[2])
		if errRemaining == nil && errTotal == nil && total > 0 {
			return capRunningTransferProgress((total - remaining) * 100 / total), true
		}
		return 0, false
	}
	percentMatches := rsyncPercentPattern.FindAllStringSubmatch(output, -1)
	if len(percentMatches) == 0 {
		return 0, false
	}
	progress, err := strconv.Atoi(percentMatches[len(percentMatches)-1][1])
	if err != nil {
		return 0, false
	}
	return capRunningTransferProgress(progress), true
}

func capRunningTransferProgress(progress int) int {
	if progress > 99 {
		progress = 99
	}
	if progress < 0 {
		progress = 0
	}
	return progress
}
