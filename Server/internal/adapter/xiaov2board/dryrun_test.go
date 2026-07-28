package xiaov2board

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestClassifyPasswordCompatibility(t *testing.T) {
	password := "secret123"
	salt := "legacy-salt"
	md5Hash := md5.Sum([]byte(password))
	sha256Hash := sha256.Sum256([]byte(password))
	md5SaltHash := md5.Sum([]byte(password + salt))
	sha256SaltHash := sha256.Sum256([]byte(password + salt))
	bcryptHash := "$2b$10$" + strings.Repeat("a", 53)
	phpBcryptHash := "$2y$10$" + strings.Repeat("b", 53)
	nPanelPBKDF2Hash := "$pbkdf2-sha512$salt$" + strings.Repeat("c", 64)

	tests := []struct {
		name string
		algo string
		hash string
		want passwordCompatibilityIssue
	}{
		{name: "md5", algo: "md5", hash: hex.EncodeToString(md5Hash[:])},
		{name: "sha256", algo: "sha256", hash: hex.EncodeToString(sha256Hash[:])},
		{name: "md5salt", algo: "md5salt", hash: hex.EncodeToString(md5SaltHash[:])},
		{name: "sha256salt", algo: "sha256salt", hash: hex.EncodeToString(sha256SaltHash[:])},
		{name: "explicit bcrypt", algo: "bcrypt", hash: bcryptHash},
		{name: "null algo PHP bcrypt", hash: phpBcryptHash},
		{name: "default PHP bcrypt", algo: "default", hash: phpBcryptHash},
		{name: "default NPanel PBKDF2", algo: "default", hash: nPanelPBKDF2Hash},
		{name: "missing hash", algo: "default", want: passwordMissingHash},
		{name: "argon2", hash: "$argon2id$v=19$m=65536,t=4,p=1$salt$hash", want: passwordArgon2},
		{name: "unknown algo", algo: "sha3", hash: "legacy-hash", want: passwordUnknownAlgo},
		{name: "unrecognized default", algo: "default", hash: "legacy-hash", want: passwordUnsupportedDefault},
		{name: "invalid NPanel PBKDF2", algo: "default", hash: "$pbkdf2-sha512$salt$encoded", want: passwordUnsupportedDefault},
		{name: "invalid bcrypt", algo: "bcrypt", hash: "$2y$broken", want: passwordInvalidHash},
		{name: "invalid md5 length", algo: "md5", hash: "abc", want: passwordInvalidHash},
		{name: "hash whitespace", algo: "bcrypt", hash: " " + bcryptHash, want: passwordInvalidHash},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyPasswordCompatibility(tt.algo, tt.hash); got != tt.want {
				t.Fatalf("classifyPasswordCompatibility(%q, %q) = %q, want %q", tt.algo, tt.hash, got, tt.want)
			}
		})
	}
}
