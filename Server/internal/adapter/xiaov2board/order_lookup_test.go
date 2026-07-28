package xiaov2board

import (
	"strings"
	"testing"
)

func TestLatestRelevantOrderJoinSQL(t *testing.T) {
	indexed := latestRelevantOrderJoinSQL(true)
	if !strings.Contains(indexed, "WHERE o2.user_id = u.id") {
		t.Fatal("indexed lookup should use the correlated point-query fast path")
	}
	if strings.Contains(indexed, "GROUP BY user_id, plan_id") {
		t.Fatal("indexed lookup should not use the aggregate fallback")
	}

	fallback := latestRelevantOrderJoinSQL(false)
	if !strings.Contains(fallback, "MAX(id) AS id") ||
		!strings.Contains(fallback, "GROUP BY user_id, plan_id") {
		t.Fatal("fallback lookup should aggregate the latest order in one scan")
	}
	if strings.Contains(fallback, "WHERE o2.user_id = u.id") {
		t.Fatal("fallback lookup must not scan v2_order once per user")
	}
}

func TestLatestRelevantOrderJoinUntilSQL(t *testing.T) {
	for _, indexed := range []bool{true, false} {
		query := latestRelevantOrderJoinUntilSQL(indexed)
		if strings.Count(query, "?") != 1 {
			t.Fatalf("indexed=%v placeholders=%d, want 1", indexed, strings.Count(query, "?"))
		}
		if !strings.Contains(query, "id <= ?") {
			t.Fatalf("indexed=%v query lacks order high-water bound", indexed)
		}
	}
}

func TestSourceOrderNo(t *testing.T) {
	if got := sourceOrderNo(42, " trade-1 "); got != "trade-1" {
		t.Fatalf("sourceOrderNo()=%q, want trade-1", got)
	}
	if got := sourceOrderNo(42, ""); got != "V2B-MIG-42" {
		t.Fatalf("sourceOrderNo()=%q, want deterministic fallback", got)
	}
}
