package connectmac

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestLocalTransferJobManagerSuccessAndFailure(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		manager := NewLocalTransferJobManager()
		job := manager.Start("mac-one", "push", func(onOutput func(string)) error {
			onOutput("  1,024  73%  1.00MB/s  0:00:01 (xfr#1, to-chk=1/4)\n")
			return nil
		})

		finished := waitForLocalTransferJob(t, manager, job.ID)
		if finished.Status != LocalTransferSucceeded || finished.Percent != 100 {
			t.Fatalf("job = %#v", finished)
		}
		if finished.StartedAt == nil || finished.FinishedAt == nil || !strings.Contains(finished.Output, "73%") {
			t.Fatalf("job timestamps/output = %#v", finished)
		}
	})

	t.Run("failure keeps real progress", func(t *testing.T) {
		manager := NewLocalTransferJobManager()
		job := manager.Start("mac-one", "pull", func(onOutput func(string)) error {
			onOutput("  1,024  41%  1.00MB/s  0:00:01\n")
			return errors.New("rsync exit status 23")
		})

		finished := waitForLocalTransferJob(t, manager, job.ID)
		if finished.Status != LocalTransferFailed || finished.Percent != 41 {
			t.Fatalf("job = %#v", finished)
		}
		if !strings.Contains(finished.Error, "exit status 23") {
			t.Fatalf("error = %q", finished.Error)
		}
	})

	t.Run("context cancellation is interrupted", func(t *testing.T) {
		manager := NewLocalTransferJobManager()
		job := manager.Start("mac-one", "pull", func(onOutput func(string)) error {
			return context.Canceled
		})
		finished := waitForLocalTransferJob(t, manager, job.ID)
		if finished.Status != LocalTransferInterrupted {
			t.Fatalf("status = %q", finished.Status)
		}
	})
}

func TestLocalTransferJobManagerDeduplicatesActiveDirection(t *testing.T) {
	manager := NewLocalTransferJobManager()
	release := make(chan struct{})
	started := make(chan struct{})
	first := manager.Start("mac-one", "push", func(onOutput func(string)) error {
		close(started)
		<-release
		return nil
	})
	<-started
	duplicate := manager.Start("mac-one", "push", func(onOutput func(string)) error {
		t.Fatal("duplicate job must not run")
		return nil
	})
	otherDirection := manager.Start("mac-one", "pull", func(onOutput func(string)) error { return nil })

	if duplicate.ID != first.ID {
		t.Fatalf("duplicate id = %q, want %q", duplicate.ID, first.ID)
	}
	if otherDirection.ID == first.ID {
		t.Fatal("different direction was deduplicated")
	}
	close(release)
	waitForLocalTransferJob(t, manager, first.ID)
	waitForLocalTransferJob(t, manager, otherDirection.ID)
}

func TestLocalTransferJobManagerCapsOutputAndCleansOldJobs(t *testing.T) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	manager := NewLocalTransferJobManager()
	manager.now = func() time.Time { return now }
	job := manager.Start("mac-one", "push", func(onOutput func(string)) error {
		onOutput(strings.Repeat("a", localTransferOutputLimit+1024))
		return nil
	})
	finished := waitForLocalTransferJob(t, manager, job.ID)
	if len(finished.Output) != localTransferOutputLimit {
		t.Fatalf("output length = %d", len(finished.Output))
	}

	now = now.Add(localTransferRetention + time.Second)
	if _, ok := manager.Get(job.ID); ok {
		t.Fatal("expired job was not cleaned")
	}
}

func TestParseRsyncProgress(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   int
		ok     bool
	}{
		{name: "percent", output: "  5,242,880  67%  12.34MB/s  0:00:01", want: 67, ok: true},
		{name: "to-chk", output: "file (xfr#3, to-chk=2/10)", want: 80, ok: true},
		{name: "to-check", output: "file (xfr#3, to-check=1/4)", want: 75, ok: true},
		{name: "running cap", output: "  5,242,880 100%  12.34MB/s  0:00:01", want: 99, ok: true},
		{name: "none", output: "sending incremental file list", ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseRsyncProgress(tt.output)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("parseRsyncProgress() = (%d, %v), want (%d, %v)", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestLocalTransferJobManagerParsesProgressAcrossOutputChunks(t *testing.T) {
	manager := NewLocalTransferJobManager()
	job := manager.Start("mac-one", "push", func(onOutput func(string)) error {
		onOutput("  1,024  5")
		onOutput("8%  1.00MB/s")
		return errors.New("exit status 23")
	})
	finished := waitForLocalTransferJob(t, manager, job.ID)
	if finished.Percent != 58 {
		t.Fatalf("percent = %d, output = %q", finished.Percent, finished.Output)
	}
}

func TestOutputCallbackWriter(t *testing.T) {
	var output string
	writer := outputCallbackWriter{onOutput: func(chunk string) { output += chunk }}
	written, err := writer.Write([]byte("rsync output"))
	if err != nil || written != len("rsync output") || output != "rsync output" {
		t.Fatalf("Write() = (%d, %v), output = %q", written, err, output)
	}
}

func waitForLocalTransferJob(t *testing.T, manager *LocalTransferJobManager, id string) LocalTransferJob {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		job, ok := manager.Get(id)
		if !ok {
			t.Fatalf("job %q not found", id)
		}
		if !job.Active() {
			return job
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for transfer job")
	return LocalTransferJob{}
}
