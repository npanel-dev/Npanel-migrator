package writer

import (
	"regexp"
	"testing"

	"npanel-migrator/internal/data/canonical"
)

func TestTargetReferCodeWithGenerator(t *testing.T) {
	t.Run("xiaov2board regenerates commercial refer code", func(t *testing.T) {
		user := &canonical.User{
			SourceID:    42,
			SourcePanel: "xiaov2board",
			ReferCode:   "10657ce63ad0a25ce079old-token",
		}

		var gotID int64
		got := targetReferCodeWithGenerator(user, func(userID int64) string {
			gotID = userID
			return "uXiao123"
		})

		if got != "uXiao123" {
			t.Fatalf("targetReferCodeWithGenerator() = %q, want commercial code", got)
		}
		if gotID != user.SourceID {
			t.Fatalf("generator user ID = %d, want %d", gotID, user.SourceID)
		}
	})

	t.Run("v2board also regenerates commercial refer code", func(t *testing.T) {
		user := &canonical.User{
			SourceID:    7,
			SourcePanel: " V2Board ",
			ReferCode:   "123456789012345678901234",
		}

		got := targetReferCodeWithGenerator(user, func(int64) string {
			return "uV2board"
		})
		if got != "uV2board" {
			t.Fatalf("targetReferCodeWithGenerator() = %q, want commercial code", got)
		}
	})

	t.Run("other panels keep existing compatibility behavior", func(t *testing.T) {
		user := &canonical.User{
			SourceID:    9,
			SourcePanel: "xboard",
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

func TestGenerateCommercialReferCode(t *testing.T) {
	format := regexp.MustCompile(`^u[A-Za-z0-9]+$`)
	seen := make(map[string]struct{}, 256)

	for id := range 256 {
		code := generateCommercialReferCode(int64(id + 1))
		if !format.MatchString(code) {
			t.Fatalf("generateCommercialReferCode() = %q, want commercial u + Base62 format", code)
		}
		if len(code) > 20 {
			t.Fatalf("generateCommercialReferCode() length = %d, exceeds refer_code MaxLen 20", len(code))
		}
		if _, exists := seen[code]; exists {
			t.Fatalf("generateCommercialReferCode() returned duplicate %q", code)
		}
		seen[code] = struct{}{}
	}
}
