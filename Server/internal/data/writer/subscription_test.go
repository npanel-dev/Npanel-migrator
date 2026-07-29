package writer

import (
	"testing"
	"time"
)

func TestSubscriptionExpireTime(t *testing.T) {
	start := time.Date(2026, time.January, 31, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		unit  string
		value int64
		want  time.Time
	}{
		{name: "day", unit: "Day", value: 7, want: start.AddDate(0, 0, 7)},
		{name: "month", unit: "Month", value: 1, want: start.AddDate(0, 1, 0)},
		{name: "quarter", unit: "quarter", value: 1, want: start.AddDate(0, 3, 0)},
		{name: "year", unit: "Year", value: 1, want: start.AddDate(1, 0, 0)},
		{name: "no limit", unit: "NoLimit", want: start.AddDate(0, 1, 0)},
		{name: "no limit alias", unit: "no_limit", want: start.AddDate(0, 1, 0)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := subscriptionExpireTime(start, tt.unit, tt.value)
			if !got.Equal(tt.want) {
				t.Fatalf("subscriptionExpireTime() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestImportedSubscriptionExpireTime(t *testing.T) {
	migrationAnchor := time.Date(2026, time.January, 15, 8, 30, 0, 0, time.UTC)
	sourceExpireTime := time.Date(2027, time.March, 20, 9, 0, 0, 0, time.UTC)

	if got := importedSubscriptionExpireTime(nil, migrationAnchor); !got.Equal(
		migrationAnchor.AddDate(0, 1, 0),
	) {
		t.Fatalf("permanent expire_time = %v, want migration anchor + 1 month", got)
	}
	if got := importedSubscriptionExpireTime(&sourceExpireTime, migrationAnchor); !got.Equal(
		sourceExpireTime,
	) {
		t.Fatalf("finite expire_time = %v, want source value %v", got, sourceExpireTime)
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
