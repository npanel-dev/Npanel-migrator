package progress

import (
	"testing"
	"time"
)

func TestTrackerReportsRateETAAndCancellation(t *testing.T) {
	tracker := NewTracker()
	if !tracker.StartJob("job-1") {
		t.Fatal("StartJob() = false")
	}
	tracker.Update(PhaseUsers, "users", 0, 100, 0)
	time.Sleep(10 * time.Millisecond)
	tracker.Update(PhaseUsers, "users", 10, 100, 0)
	snapshot := tracker.Snapshot()
	if snapshot.JobID != "job-1" || snapshot.RatePerSecond <= 0 || snapshot.ETASeconds <= 0 {
		t.Fatalf("unexpected rate snapshot: %+v", snapshot)
	}
	tracker.Update(PhaseOrders, "orders", 0, 50, 0)
	snapshot = tracker.Snapshot()
	if snapshot.RatePerSecond != 0 || snapshot.ETASeconds != 0 {
		t.Fatalf("new phase retained stale rate/ETA: %+v", snapshot)
	}
	tracker.SetCancelRequested(true)
	tracker.Cancel("cancelled")
	snapshot = tracker.Snapshot()
	if snapshot.Status != StatusCancelled || !snapshot.Resumable || !snapshot.CancelRequested {
		t.Fatalf("unexpected cancelled snapshot: %+v", snapshot)
	}
}
