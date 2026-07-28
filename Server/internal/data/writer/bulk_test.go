package writer

import (
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/npanel-dev/NPanel-backend/ent"

	"npanel-migrator/internal/data/canonical"
)

var benchmarkUserBuilders []*ent.ProxyUserCreate

func TestExecuteBulkWithBisectIsolatesBadRows(t *testing.T) {
	items := make([]int, 1000)
	for index := range items {
		items[index] = index + 1
	}
	bad := map[int]bool{123: true, 777: true}
	committed := make([]int, 0, len(items)-len(bad))
	rejected := make([]int, 0, len(bad))

	failed, err := executeBulkWithBisect(
		items,
		func(batch []int) error {
			for _, id := range batch {
				if bad[id] {
					return &batchDataError{message: fmt.Sprintf("bad row %d", id)}
				}
			}
			committed = append(committed, batch...)
			return nil
		},
		func(id int, _ error) error {
			rejected = append(rejected, id)
			return nil
		},
	)
	if err != nil {
		t.Fatalf("executeBulkWithBisect() error = %v", err)
	}
	if failed != 2 || !slices.Equal(rejected, []int{123, 777}) {
		t.Fatalf("failed=%d rejected=%v, want 2 and [123 777]", failed, rejected)
	}
	if len(committed) != 998 {
		t.Fatalf("committed=%d, want 998", len(committed))
	}
	for _, id := range committed {
		if bad[id] {
			t.Fatalf("bad row %d was committed", id)
		}
	}
}

func TestExecuteBulkWithBisectDoesNotSplitInfrastructureFailure(t *testing.T) {
	infrastructureErr := errors.New("connection reset")
	executeCalls := 0
	rejectCalls := 0
	_, err := executeBulkWithBisect(
		[]int{1, 2, 3, 4},
		func([]int) error {
			executeCalls++
			return infrastructureErr
		},
		func(int, error) error {
			rejectCalls++
			return nil
		},
	)
	if !errors.Is(err, infrastructureErr) {
		t.Fatalf("error=%v, want infrastructure error", err)
	}
	if executeCalls != 1 || rejectCalls != 0 {
		t.Fatalf("executeCalls=%d rejectCalls=%d, want 1 and 0", executeCalls, rejectCalls)
	}
}

// TestPowerLossResumeFromCommittedCursor models a process loss immediately
// before one batch commit. Only committed batches advance the cursor; replay
// therefore resumes at the first uncommitted row without duplicating prior rows.
func TestPowerLossResumeFromCommittedCursor(t *testing.T) {
	const (
		total     = 85_000
		batchSize = 1000
		crashAt   = 37
	)
	committed := make([]int, total+1)
	cursor := 0
	batchNumber := 0

	run := func(injectCrash bool) error {
		for cursor < total {
			end := min(cursor+batchSize, total)
			batch := make([]int, end-cursor)
			for index := range batch {
				batch[index] = cursor + index + 1
			}
			_, err := executeBulkWithBisect(
				batch,
				func(candidate []int) error {
					if injectCrash && batchNumber == crashAt {
						return errors.New("injected power loss before commit")
					}
					for _, id := range candidate {
						committed[id]++
					}
					cursor = candidate[len(candidate)-1]
					batchNumber++
					return nil
				},
				func(int, error) error { return nil },
			)
			if err != nil {
				return err
			}
		}
		return nil
	}

	if err := run(true); err == nil {
		t.Fatal("first run error=nil, want injected power loss")
	}
	if cursor != crashAt*batchSize {
		t.Fatalf("cursor=%d, want last committed cursor %d", cursor, crashAt*batchSize)
	}
	if err := run(false); err != nil {
		t.Fatalf("resume error=%v", err)
	}
	for id := 1; id <= total; id++ {
		if committed[id] != 1 {
			t.Fatalf("row %d committed %d times, want exactly once", id, committed[id])
		}
	}
}

func BenchmarkBulkControlPath85000(b *testing.B) {
	const (
		total     = 85_000
		batchSize = 1000
	)
	items := make([]int, total)
	for index := range items {
		items[index] = index + 1
	}
	b.ReportAllocs()
	b.SetBytes(total)
	for range b.N {
		calls := 0
		for start := 0; start < total; start += batchSize {
			end := min(start+batchSize, total)
			_, err := executeBulkWithBisect(
				items[start:end],
				func([]int) error {
					calls++
					return nil
				},
				func(int, error) error { return nil },
			)
			if err != nil {
				b.Fatal(err)
			}
		}
		if calls != 85 {
			b.Fatalf("bulk calls=%d, want 85", calls)
		}
	}
}

// BenchmarkBuildUserBulk85000 measures the CPU/allocation side of preparing
// 85,000 Ent user rows in 1,000-row batches. It intentionally excludes network
// and MySQL execution, which must be measured in the customer environment.
func BenchmarkBuildUserBulk85000(b *testing.B) {
	const (
		total     = 85_000
		batchSize = 1000
	)
	now := time.Unix(1_700_000_000, 0)
	users := make([]*canonical.User, total)
	for index := range users {
		id := int64(index + 1)
		users[index] = &canonical.User{
			SourceID: id, Email: fmt.Sprintf("user-%d@example.com", id),
			PasswordHash: "$2y$10$example", PasswordAlgo: "bcrypt",
			Enabled: true, CreatedAt: now, UpdatedAt: now,
		}
	}
	client := ent.NewClient()
	b.ReportAllocs()
	for range b.N {
		builders := make([]*ent.ProxyUserCreate, 0, total)
		for start := 0; start < total; start += batchSize {
			end := min(start+batchSize, total)
			for _, user := range users[start:end] {
				builders = append(builders, newUserBuilder(client, user))
			}
		}
		benchmarkUserBuilders = builders
	}
}
