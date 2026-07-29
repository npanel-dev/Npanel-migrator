package xiaov2board

import (
	"testing"
	"time"
)

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

func TestSubscriptionSourceTimes(t *testing.T) {
	const (
		orderCreated = int64(1_700_000_000)
		orderPaid    = int64(1_700_000_120)
		userCreated  = int64(1_600_000_000)
		evaluation   = int64(1_800_000_000)
	)
	tests := []struct {
		name            string
		paidAt          int64
		orderCreatedAt  int64
		userCreatedAt   int64
		wantStartUnix   int64
		wantCreatedUnix int64
	}{
		{
			name:            "preserves order payment and creation times",
			paidAt:          orderPaid,
			orderCreatedAt:  orderCreated,
			userCreatedAt:   userCreated,
			wantStartUnix:   orderPaid,
			wantCreatedUnix: orderCreated,
		},
		{
			name:            "falls back to order creation for start time",
			orderCreatedAt:  orderCreated,
			userCreatedAt:   userCreated,
			wantStartUnix:   orderCreated,
			wantCreatedUnix: orderCreated,
		},
		{
			name:            "falls back to user creation",
			userCreatedAt:   userCreated,
			wantStartUnix:   userCreated,
			wantCreatedUnix: userCreated,
		},
		{
			name:            "uses fixed evaluation time only when source times are absent",
			wantStartUnix:   evaluation,
			wantCreatedUnix: evaluation,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, created := subscriptionSourceTimes(
				tt.paidAt, tt.orderCreatedAt, tt.userCreatedAt, evaluation,
			)
			if got := start.Unix(); got != tt.wantStartUnix {
				t.Fatalf("start.Unix() = %d, want %d", got, tt.wantStartUnix)
			}
			if got := created.Unix(); got != tt.wantCreatedUnix {
				t.Fatalf("created.Unix() = %d, want %d", got, tt.wantCreatedUnix)
			}
			if start.Location() != time.Local || created.Location() != time.Local {
				t.Fatalf("source times should retain unixToTime location semantics")
			}
		})
	}
}
