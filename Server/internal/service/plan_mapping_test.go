package service

import (
	"strconv"
	"testing"
)

func TestKnownSourcePeriods(t *testing.T) {
	periods := knownSourcePeriods()
	want := map[string]string{
		"month_price":      "Month:1",
		"quarter_price":    "Month:3",
		"half_year_price":  "Month:6",
		"year_price":       "Year:1",
		"two_year_price":   "Year:2",
		"three_year_price": "Year:3",
		"onetime_price":    "NoLimit:0",
		"reset_price":      ":0",
	}
	if len(periods) != len(want) {
		t.Fatalf("knownSourcePeriods() returned %d periods, want %d", len(periods), len(want))
	}
	for _, period := range periods {
		signature, ok := want[period.SourcePeriod]
		if !ok {
			t.Fatalf("unexpected source period %q", period.SourcePeriod)
		}
		got := period.DurationUnit + ":" + strconv.FormatInt(period.DurationValue, 10)
		if got != signature {
			t.Fatalf("%s signature = %q, want %q", period.SourcePeriod, got, signature)
		}
		delete(want, period.SourcePeriod)
	}
}

func TestNormalizeTrialTimeUnit(t *testing.T) {
	tests := map[string]string{
		"day": "Day", "Month": "Month", "no_limit": "NoLimit",
		"quarter": "quarter", "half_year": "half_year",
	}
	for input, want := range tests {
		if got := normalizeTrialTimeUnit(input); got != want {
			t.Fatalf("normalizeTrialTimeUnit(%q) = %q, want %q", input, got, want)
		}
	}
}
