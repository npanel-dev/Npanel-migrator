package writer

import (
	"regexp"
	"testing"

	"npanel-migrator/internal/data/canonical"
)

func TestTargetReferCodeWithGenerator(t *testing.T) {
	t.Run("xiaov2board regenerates NPanel refer code", func(t *testing.T) {
		user := &canonical.User{
			SourceID:    42,
			SourcePanel: "xiaov2board",
			ReferCode:   "10657ce63ad0a25ce079old-token",
		}

		var gotID int64
		got := targetReferCodeWithGenerator(user, func(userID int64) string {
			gotID = userID
			return "ABCD-EFGH-IJKL"
		})

		if got != "ABCD-EFGH-IJKL" {
			t.Fatalf("targetReferCodeWithGenerator() = %q, want NPanel code", got)
		}
		if gotID != user.SourceID {
			t.Fatalf("generator user ID = %d, want %d", gotID, user.SourceID)
		}
	})

	t.Run("source panel matching is whitespace and case insensitive", func(t *testing.T) {
		user := &canonical.User{
			SourceID:    7,
			SourcePanel: " XiaoV2Board ",
			ReferCode:   "legacy",
		}

		got := targetReferCodeWithGenerator(user, func(int64) string {
			return "NPAN-ELCO-DE"
		})
		if got != "NPAN-ELCO-DE" {
			t.Fatalf("targetReferCodeWithGenerator() = %q, want generated code", got)
		}
	})

	t.Run("other panels keep existing compatibility behavior", func(t *testing.T) {
		user := &canonical.User{
			SourceID:    9,
			SourcePanel: "v2board",
			ReferCode:   "123456789012345678901234",
		}

		called := false
		got := targetReferCodeWithGenerator(user, func(int64) string {
			called = true
			return "unused"
		})

		if called {
			t.Fatal("generator must not be called for other panels")
		}
		if got != "12345678901234567890" {
			t.Fatalf("targetReferCodeWithGenerator() = %q, want legacy truncation", got)
		}
	})
}

func TestGenerateNPanelReferCode(t *testing.T) {
	format := regexp.MustCompile(`^[A-Z0-9]{4}(?:-[A-Z0-9]{1,4})+$`)
	seen := make(map[string]struct{}, 256)

	for range 256 {
		code := generateNPanelReferCode(0)
		if !format.MatchString(code) {
			t.Fatalf("generateNPanelReferCode() = %q, want NPanel dashed Base36 format", code)
		}
		if len(code) > 20 {
			t.Fatalf("generateNPanelReferCode() length = %d, exceeds refer_code MaxLen 20", len(code))
		}
		if _, exists := seen[code]; exists {
			t.Fatalf("generateNPanelReferCode() returned duplicate %q", code)
		}
		seen[code] = struct{}{}
	}
}
