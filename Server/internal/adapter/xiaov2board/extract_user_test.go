package xiaov2board

import (
	"strings"
	"testing"
)

func TestNormalizeAlgo(t *testing.T) {
	phpBcryptHash := "$2y$10$" + strings.Repeat("a", 53)
	goBcryptHash := "$2b$10$" + strings.Repeat("b", 53)
	tests := []struct {
		name string
		algo string
		hash string
		want string
	}{
		{name: "md5", algo: "md5", want: "md5"},
		{name: "sha256", algo: "sha256", want: "sha256"},
		{name: "md5salt", algo: "md5salt", want: "md5salt"},
		{name: "sha256salt", algo: "sha256salt", want: "sha256salt"},
		{name: "bcrypt explicit", algo: "bcrypt", want: "bcrypt"},
		{name: "default explicit PHP bcrypt", algo: "default", hash: phpBcryptHash, want: "bcrypt"},
		{name: "default explicit NPanel PBKDF2", algo: "default", hash: "$pbkdf2-sha512$salt$hash", want: "default"},
		{name: "case and space tolerant", algo: " SHA256SALT ", want: "sha256salt"},
		{name: "null algo bcrypt 2y", hash: phpBcryptHash, want: "bcrypt"},
		{name: "null algo bcrypt 2b", hash: goBcryptHash, want: "bcrypt"},
		{name: "invalid bcrypt prefix only", algo: "default", hash: "$2y$broken", want: "default"},
		{name: "null algo unknown", hash: "legacy-hash", want: "default"},
		{name: "unknown algo preserved", algo: "sha3", hash: "legacy-hash", want: "sha3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeAlgo(tt.algo, tt.hash); got != tt.want {
				t.Fatalf("normalizeAlgo(%q, %q) = %q, want %q", tt.algo, tt.hash, got, tt.want)
			}
		})
	}
}
