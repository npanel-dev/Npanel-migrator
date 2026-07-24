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
