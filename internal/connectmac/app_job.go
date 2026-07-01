package connectmac

import (
	"context"
	"fmt"
	"time"
)

func (a App) runJob(ctx context.Context, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(a.Err, "usage: cm job <list|status|log|wait> [job-id]")
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
	default:
		fmt.Fprintf(a.Err, "unknown job command %q\n", args[0])
		return 2
	}
}

func (a App) runJobWait(ctx context.Context, id string) int {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		job, err := a.JobManager.Load(id)
		if err != nil {
			fmt.Fprintf(a.Err, "job wait failed: %v\n", err)
			return 1
		}
		if job.Status != JobStatusRunning {
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
