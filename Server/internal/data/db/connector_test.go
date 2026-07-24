package db

import "testing"

func TestHasIndexPrefix(t *testing.T) {
	indexes := map[string][]string{
		"PRIMARY":              {"id"},
		"idx_user_plan_id":     {"user_id", "plan_id", "id"},
		"idx_status_user_plan": {"status", "user_id", "plan_id"},
	}

	if !hasIndexPrefix(indexes, []string{"user_id", "plan_id", "id"}) {
		t.Fatal("expected exact composite index prefix to match")
	}
	if !hasIndexPrefix(indexes, []string{"USER_ID", "PLAN_ID"}) {
		t.Fatal("expected case-insensitive shorter prefix to match")
	}
	if hasIndexPrefix(indexes, []string{"plan_id", "user_id"}) {
		t.Fatal("index column order must be respected")
	}
}
