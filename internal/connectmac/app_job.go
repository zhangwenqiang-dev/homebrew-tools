package connectmac

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	defaultJobWaitAllTimeout  = 2 * time.Hour
	defaultJobWaitAllInterval = 10 * time.Second
)

func (a App) runJob(ctx context.Context, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(a.Err, "usage: cm job <list|status|log|wait|active|wait-all>")
		return 2
	}
	switch args[0] {
	case "list":
		if len(args) != 1 {
			fmt.Fprintln(a.Err, "usage: cm job list")
			return 2
		}
		jobs, err := a.JobManager.List()
		if err != nil {
			fmt.Fprintf(a.Err, "job list failed: %v\n", err)
			return 1
		}
		if len(jobs) == 0 {
			fmt.Fprintln(a.Out, "No jobs.")
			return 0
		}
		fmt.Fprint(a.Out, formatJobsTable(jobs))
		return 0
	case "status":
		if len(args) != 2 {
			fmt.Fprintln(a.Err, "usage: cm job status <job-id>")
			return 2
		}
		job, err := a.JobManager.Load(args[1])
		if err != nil {
			fmt.Fprintf(a.Err, "job status failed: %v\n", err)
			return 1
		}
		printJobStatus(a.Out, job)
		return 0
	case "log":
		if len(args) != 2 {
			fmt.Fprintln(a.Err, "usage: cm job log <job-id>")
			return 2
		}
		job, err := a.JobManager.Load(args[1])
		if err != nil {
			fmt.Fprintf(a.Err, "job log failed: %v\n", err)
			return 1
		}
		if err := copyTail(a.Out, job.Log, 64*1024); err != nil {
			fmt.Fprintf(a.Err, "job log failed: %v\n", err)
			return 1
		}
		return 0
	case "wait":
		if len(args) != 2 {
			fmt.Fprintln(a.Err, "usage: cm job wait <job-id>")
			return 2
		}
		return a.runJobWait(ctx, args[1])
	case "run":
		if len(args) != 2 {
			fmt.Fprintln(a.Err, "usage: cm job run <job-id>")
			return 2
		}
		_, err := a.JobManager.RunJob(ctx, args[1])
		if err != nil {
			fmt.Fprintf(a.Err, "job run failed: %v\n", err)
			return 1
		}
		return 0
	case "active":
		return a.runJobActive(args[1:])
	case "wait-all":
		return a.runJobWaitAll(ctx, args[1:])
	case "end-drain":
		if len(args) != 1 {
			fmt.Fprintln(a.Err, "usage: cm job end-drain")
			return 2
		}
		if err := a.JobManager.EndDrain(); err != nil {
			fmt.Fprintf(a.Err, "job end-drain failed: %v\n", err)
			return 1
		}
		return 0
	default:
		fmt.Fprintf(a.Err, "unknown job command %q\n", args[0])
		return 2
	}
}

func (a App) runJobActive(args []string) int {
	jsonOutput := false
	switch {
	case len(args) == 0:
	case len(args) == 1 && args[0] == "--json":
		jsonOutput = true
	default:
		fmt.Fprintln(a.Err, "usage: cm job active [--json]")
		return 2
	}
	jobs, err := a.JobManager.Active()
	if err != nil {
		fmt.Fprintf(a.Err, "job active failed: %v\n", err)
		return 1
	}
	if jsonOutput {
		if err := json.NewEncoder(a.Out).Encode(jobs); err != nil {
			fmt.Fprintf(a.Err, "job active failed: %v\n", err)
			return 1
		}
		return 0
	}
	if len(jobs) == 0 {
		fmt.Fprintln(a.Out, "No active jobs.")
		return 0
	}
	fmt.Fprint(a.Out, formatJobsTable(jobs))
	return 0
}

func (a App) runJobWaitAll(ctx context.Context, args []string) int {
	timeout, interval, drain, err := parseJobWaitAllArgs(args)
	if err != nil {
		fmt.Fprintf(a.Err, "job wait-all: %v\n", err)
		fmt.Fprintln(a.Err, "usage: cm job wait-all [--timeout 2h] [--interval 10s] [--drain]")
		return 2
	}
	if drain {
		if err := a.JobManager.BeginDrain(); err != nil {
			fmt.Fprintf(a.Err, "job wait-all failed to begin drain: %v\n", err)
			return 1
		}
	}
	err = a.JobManager.WaitAll(ctx, timeout, interval, func(elapsed time.Duration, active []Job) {
		fmt.Fprintf(a.Out, "Waiting for background jobs: elapsed=%s active=%s\n", elapsed, strings.Join(jobIDsForOutput(active), ","))
	})
	if err == nil {
		fmt.Fprintln(a.Out, "All background jobs completed.")
		return 0
	}
	if drain {
		if cleanupErr := a.JobManager.EndDrain(); cleanupErr != nil {
			fmt.Fprintf(a.Err, "job wait-all failed to end drain: %v\n", cleanupErr)
		}
	}
	var timeoutErr *WaitAllTimeoutError
	if errors.As(err, &timeoutErr) {
		fmt.Fprintf(a.Err, "job wait-all timed out after %s; active jobs: %s\n", timeoutErr.Timeout, strings.Join(jobIDsForOutput(timeoutErr.Active), ", "))
		return 1
	}
	fmt.Fprintf(a.Err, "job wait-all failed: %v\n", err)
	return 1
}

func parseJobWaitAllArgs(args []string) (time.Duration, time.Duration, bool, error) {
	timeout := defaultJobWaitAllTimeout
	interval := defaultJobWaitAllInterval
	drain := false
	seenTimeout := false
	seenInterval := false
	for i := 0; i < len(args); i++ {
		flag := args[i]
		if flag == "--drain" {
			if drain {
				return 0, 0, false, errors.New("--drain may only be specified once")
			}
			drain = true
			continue
		}
		if flag != "--timeout" && flag != "--interval" {
			return 0, 0, false, fmt.Errorf("unknown argument %q", flag)
		}
		if i+1 >= len(args) {
			return 0, 0, false, fmt.Errorf("%s requires a duration", flag)
		}
		i++
		duration, err := time.ParseDuration(args[i])
		if err != nil {
			return 0, 0, false, fmt.Errorf("invalid %s duration %q: %w", strings.TrimPrefix(flag, "--"), args[i], err)
		}
		if duration <= 0 {
			return 0, 0, false, fmt.Errorf("%s must be positive", strings.TrimPrefix(flag, "--"))
		}
		switch flag {
		case "--timeout":
			if seenTimeout {
				return 0, 0, false, errors.New("--timeout may only be specified once")
			}
			seenTimeout = true
			timeout = duration
		case "--interval":
			if seenInterval {
				return 0, 0, false, errors.New("--interval may only be specified once")
			}
			seenInterval = true
			interval = duration
		}
	}
	return timeout, interval, drain, nil
}

func jobIDsForOutput(jobs []Job) []string {
	ids := make([]string, len(jobs))
	for i, job := range jobs {
		ids[i] = job.ID
	}
	return ids
}

func (a App) runJobWait(ctx context.Context, id string) int {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		if _, err := a.JobManager.Reconcile(); err != nil {
			fmt.Fprintf(a.Err, "job wait failed: %v\n", err)
			return 1
		}
		job, err := a.JobManager.Load(id)
		if err != nil {
			fmt.Fprintf(a.Err, "job wait failed: %v\n", err)
			return 1
		}
		if job.Status != JobStatusStarting && job.Status != JobStatusRunning {
			printJobStatus(a.Out, job)
			if job.Status == JobStatusSuccess || job.Status == JobStatusDeferred {
				return 0
			}
			return 1
		}
		select {
		case <-ctx.Done():
			fmt.Fprintf(a.Err, "job wait canceled: %v\n", ctx.Err())
			return 1
		case <-ticker.C:
		}
	}
}

func printJobStatus(out interface{ Write([]byte) (int, error) }, job Job) {
	fmt.Fprintf(out, "Job: %s\n", job.ID)
	fmt.Fprintf(out, "Type: %s\n", job.Type)
	fmt.Fprintf(out, "Profile: %s\n", job.Profile)
	if job.AppleEmail != "" {
		fmt.Fprintf(out, "Apple account: %s\n", job.AppleEmail)
	}
	fmt.Fprintf(out, "Status: %s\n", job.Status)
	if job.PID > 0 {
		fmt.Fprintf(out, "PID: %d\n", job.PID)
	}
	if !job.StartedAt.IsZero() {
		fmt.Fprintf(out, "Started: %s\n", job.StartedAt.Format(time.RFC3339))
	}
	if !job.FinishedAt.IsZero() {
		fmt.Fprintf(out, "Finished: %s\n", job.FinishedAt.Format(time.RFC3339))
	}
	if job.Log != "" {
		fmt.Fprintf(out, "Log: %s\n", job.Log)
	}
	if job.LastError != "" {
		fmt.Fprintf(out, "Error: %s\n", job.LastError)
	}
}
