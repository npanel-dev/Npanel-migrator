package xiaov2board

import "testing"

func TestNormalizeAlgo(t *testing.T) {
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
		{name: "default explicit", algo: "default", want: "default"},
		{name: "case and space tolerant", algo: " SHA256SALT ", want: "sha256salt"},
		{name: "null algo bcrypt 2y", hash: "$2y$10$abcdefghijklmnopqrstuu7G2Zx9Q4", want: "bcrypt"},
		{name: "null algo bcrypt 2b", hash: "$2b$10$abcdefghijklmnopqrstuu7G2Zx9Q4", want: "bcrypt"},
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
