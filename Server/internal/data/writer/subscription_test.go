package writer

import (
	"testing"
	"time"

	"github.com/npanel-dev/NPanel-backend/ent"
)

func TestSubscriptionExpireTime(t *testing.T) {
	start := time.Date(2026, time.January, 31, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		unit  string
		value int64
		want  *time.Time
	}{
		{name: "day", unit: "Day", value: 7, want: timePointer(start.AddDate(0, 0, 7))},
		{name: "month", unit: "Month", value: 1, want: timePointer(start.AddDate(0, 1, 0))},
		{name: "quarter", unit: "quarter", value: 1, want: timePointer(start.AddDate(0, 3, 0))},
		{name: "year", unit: "Year", value: 1, want: timePointer(start.AddDate(1, 0, 0))},
		{name: "no limit", unit: "NoLimit", want: nil},
		{name: "no limit alias", unit: "no_limit", want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := subscriptionExpireTime(start, tt.unit, tt.value)
			if tt.want == nil {
				if got != nil {
					t.Fatalf("subscriptionExpireTime() = %v, want nil", got)
				}
				return
			}
			if got == nil || !got.Equal(*tt.want) {
				t.Fatalf("subscriptionExpireTime() = %v, want %v", got, tt.want)
			}
		})
	}
}

func timePointer(value time.Time) *time.Time {
	return &value
}

func TestNillableSubscriptionExpiryLeavesUnlimitedSubscriptionsNull(t *testing.T) {
	client := ent.NewClient()
	start := time.Date(2026, time.January, 31, 12, 0, 0, 0, time.UTC)

	unlimited := client.ProxyUserSubscribe.Create().
		SetNillableExpireTime(subscriptionExpireTime(start, "NoLimit", 0))
	if _, exists := unlimited.Mutation().ExpireTime(); exists {
		t.Fatal("NoLimit subscription set expire_time, want database NULL")
	}

	finite := client.ProxyUserSubscribe.Create().
		SetNillableExpireTime(subscriptionExpireTime(start, "Day", 7))
	got, exists := finite.Mutation().ExpireTime()
	want := start.AddDate(0, 0, 7)
	if !exists || !got.Equal(want) {
		t.Fatalf("finite expire_time = %v, exists=%v, want %v", got, exists, want)
	}
}

func TestTrialTokenIsDeterministicPerTargetUser(t *testing.T) {
	first := trialToken(42)
	if second := trialToken(42); first != second {
		t.Fatalf("trial token changed for same user: %q != %q", first, second)
	}
	if other := trialToken(43); first == other {
		t.Fatalf("trial token must differ across users: %q", first)
	}
	if len(first) != 32 {
		t.Fatalf("trial token length = %d, want 32", len(first))
	}
}
