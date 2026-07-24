package xiaov2board

import "testing"

func TestSubscriptionNeedsTrial(t *testing.T) {
	const now = int64(1_700_000_000)
	tests := []struct {
		name         string
		planValid    bool
		planID       int64
		traffic      int64
		expiredValid bool
		expiredAt    int64
		want         bool
	}{
		{name: "no plan", planValid: false, traffic: 1024, want: true},
		{name: "zero plan id", planValid: true, traffic: 1024, want: true},
		{name: "zero traffic", planValid: true, planID: 1, want: true},
		{name: "null expiration is active", planValid: true, planID: 1, traffic: 1024, want: false},
		{name: "zero expiration is inactive", planValid: true, planID: 1, traffic: 1024, expiredValid: true, want: true},
		{name: "past expiration", planValid: true, planID: 1, traffic: 1024, expiredValid: true, expiredAt: now - 1, want: true},
		{name: "future expiration", planValid: true, planID: 1, traffic: 1024, expiredValid: true, expiredAt: now + 1, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := subscriptionNeedsTrial(
				tt.planValid, tt.planID, tt.traffic,
				tt.expiredValid, tt.expiredAt, now,
			)
			if got != tt.want {
				t.Fatalf("subscriptionNeedsTrial() = %v, want %v", got, tt.want)
			}
		})
	}
}
