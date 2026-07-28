package checkpoint

import (
	"testing"
	"time"
)

func TestDeriveJobState(t *testing.T) {
	now := time.Now()
	expired := now.Add(-time.Second)
	future := now.Add(time.Minute)
	tests := []struct {
		name      string
		job       Job
		effective string
		resumable bool
	}{
		{
			name:      "expired running lease is interrupted",
			job:       Job{Status: StatusRunning, LeaseUntil: &expired},
			effective: "interrupted", resumable: true,
		},
		{
			name:      "active running lease is not resumable",
			job:       Job{Status: StatusRunning, LeaseUntil: &future},
			effective: StatusRunning,
		},
		{
			name:      "failed task is resumable",
			job:       Job{Status: StatusFailed},
			effective: StatusFailed, resumable: true,
		},
		{
			name:      "completed task is final",
			job:       Job{Status: StatusCompleted},
			effective: StatusCompleted,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deriveJobState(&tt.job, now)
			if tt.job.EffectiveStatus != tt.effective || tt.job.Resumable != tt.resumable {
				t.Fatalf(
					"effective=%q resumable=%v, want %q %v",
					tt.job.EffectiveStatus, tt.job.Resumable, tt.effective, tt.resumable,
				)
			}
		})
	}
}
