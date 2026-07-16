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
	ErrLocalTransferConflict = errors.New("active local transfer has a different transfer ID")
	rsyncPercentPattern      = regexp.MustCompile(`(?:^|\s)(\d{1,3})%`)
	rsyncToCheckPattern      = regexp.MustCompile(`to-(?:chk|check)=(\d+)/(\d+)`)
)

type LocalTransferJob struct {
	ID                string     `json:"id"`
	TransferID        string     `json:"transfer_id,omitempty"`
	Profile           string     `json:"profile"`
	Direction         string     `json:"direction"`
	Status            string     `json:"status"`
	Percent           int        `json:"percent"`
	Output            string     `json:"output"`
	Error             string     `json:"error"`
	CreatedAt         time.Time  `json:"created_at"`
	StartedAt         *time.Time `json:"started_at"`
	FinishedAt        *time.Time `json:"finished_at"`
	onEvent           func(LocalTransferEvent)
	emittedMilestones map[int]bool
}

func (j LocalTransferJob) Active() bool {
	return j.Status == LocalTransferQueued || j.Status == LocalTransferRunning
}

type LocalTransferEvent struct {
	TransferID string
	LocalJobID string
	Profile    string
	Direction  string
	Status     string
	Percent    int
	Elapsed    time.Duration
	Error      string
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
	return m.StartWithEvents("", profile, direction, nil, run)
}

func (m *LocalTransferJobManager) StartWithEvents(transferID, profile, direction string, onEvent func(LocalTransferEvent), run func(func(string)) error) (LocalTransferJob, error) {
	m.mu.Lock()
	m.cleanupLocked()
	if m.draining {
		m.mu.Unlock()
		return LocalTransferJob{}, ErrLocalTransferDraining
	}
	for _, job := range m.jobs {
		if job.Profile == profile && job.Direction == direction && job.Active() {
			if transferID != "" && job.TransferID != "" && transferID != job.TransferID {
				m.mu.Unlock()
				return LocalTransferJob{}, fmt.Errorf("%w: active=%s requested=%s", ErrLocalTransferConflict, job.TransferID, transferID)
			}
			result := *job
			m.mu.Unlock()
			return result, nil
		}
	}
	m.sequence++
	created := m.now()
	job := &LocalTransferJob{
		ID:                fmt.Sprintf("transfer-%d-%d", created.UnixNano(), m.sequence),
		TransferID:        transferID,
		Profile:           profile,
		Direction:         direction,
		Status:            LocalTransferQueued,
		CreatedAt:         created,
		onEvent:           onEvent,
		emittedMilestones: make(map[int]bool),
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
	job.emittedMilestones[0] = true
	event := localTransferEvent(*job, started)
	m.mu.Unlock()
	emitLocalTransferEvent(job.onEvent, event)

	err := run(func(output string) {
		m.appendOutput(id, output)
	})

	m.mu.Lock()
	job, ok = m.jobs[id]
	if !ok {
		m.mu.Unlock()
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
	event = localTransferEvent(*job, finished)
	onEvent := job.onEvent
	m.mu.Unlock()
	emitLocalTransferEvent(onEvent, event)
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
	job, ok := m.jobs[id]
	if !ok {
		m.mu.Unlock()
		return
	}
	job.Output += output
	if len(job.Output) > localTransferOutputLimit {
		job.Output = job.Output[len(job.Output)-localTransferOutputLimit:]
	}
	if progress, ok := parseRsyncProgress(job.Output); ok && progress > job.Percent {
		job.Percent = progress
	}
	events := job.milestoneEvents(m.now())
	onEvent := job.onEvent
	m.mu.Unlock()
	for _, event := range events {
		emitLocalTransferEvent(onEvent, event)
	}
}

func (j *LocalTransferJob) milestoneEvents(now time.Time) []LocalTransferEvent {
	var events []LocalTransferEvent
	for _, milestone := range []int{10, 25, 50, 75, 90, 99} {
		if j.Percent < milestone || j.emittedMilestones[milestone] {
			continue
		}
		j.emittedMilestones[milestone] = true
		event := localTransferEvent(*j, now)
		event.Percent = milestone
		events = append(events, event)
	}
	return events
}

func localTransferEvent(job LocalTransferJob, now time.Time) LocalTransferEvent {
	elapsed := time.Duration(0)
	if job.StartedAt != nil {
		elapsed = now.Sub(*job.StartedAt)
		if elapsed < 0 {
			elapsed = 0
		}
	}
	return LocalTransferEvent{
		TransferID: job.TransferID,
		LocalJobID: job.ID,
		Profile:    job.Profile,
		Direction:  job.Direction,
		Status:     job.Status,
		Percent:    job.Percent,
		Elapsed:    elapsed,
		Error:      job.Error,
	}
}

func emitLocalTransferEvent(callback func(LocalTransferEvent), event LocalTransferEvent) {
	if callback != nil {
		callback(event)
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
