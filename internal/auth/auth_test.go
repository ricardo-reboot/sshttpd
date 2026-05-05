package auth

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bugscave/sshttpd/internal/config"
)

func TestIsCommandAllowed_Anonymous(t *testing.T) {
	cfg := &config.AuthConfig{
		Anonymous:  []string{"receive-pack", "api-call GET", "rss-feed"},
		Identified: []string{"comment", "upvote"},
		Trusted:    []string{"flag", "admin-*"},
	}

	cases := []struct {
		tier    string
		command string
		want    bool
	}{
		{TierAnonymous, "receive-pack", true},
		{TierAnonymous, "receive-pack /", true},                  // verb-only entry matches qualified command
		{TierAnonymous, "api-call GET /api/items", true},         // multi-part entry matches prefix
		{TierAnonymous, "api-call POST /api/items", false},       // method mismatch
		{TierAnonymous, "comment 42 hi", false},                  // identified-only command
		{TierAnonymous, "rss-feed /feeds/posts", true},
		{TierIdentified, "comment 42 hi", true},                  // identified inherits anonymous
		{TierIdentified, "receive-pack /", true},                 // identified inherits anonymous
		{TierIdentified, "flag 99", false},                       // trusted-only
		{TierTrusted, "flag 99", true},
		{TierTrusted, "admin-purge", true},                       // wildcard
		{TierTrusted, "admin-anything-else", true},
		{TierTrusted, "admin", false},                            // wildcard requires the prefix to be present
		{TierAnonymous, "unknown", false},
	}

	for _, c := range cases {
		got := IsCommandAllowed(c.tier, c.command, cfg)
		if got != c.want {
			t.Errorf("IsCommandAllowed(%q, %q) = %v, want %v", c.tier, c.command, got, c.want)
		}
	}
}

func TestLoadAuthorizedKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "authorized_keys")
	contents := `
# A comment
tier=trusted ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIDdlxNyaxUq2/H59CxBZxOwwR3SjUTGwLhc8dbpibAhk admin@example
ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIIeYPF6w0iiOHC8R1lCO4DaZkY6XjVkuLm0lgTL5RSt9 regular@example
`
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}

	ak, err := LoadAuthorizedKeys(path)
	if err != nil {
		t.Fatalf("LoadAuthorizedKeys: %v", err)
	}
	if len(ak.entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(ak.entries))
	}

	// Find which fingerprint maps to which tier (no need to compute fingerprints in the test).
	var trustedCount, identifiedCount int
	for _, tier := range ak.entries {
		switch tier {
		case TierTrusted:
			trustedCount++
		case TierIdentified:
			identifiedCount++
		}
	}
	if trustedCount != 1 || identifiedCount != 1 {
		t.Errorf("expected 1 trusted + 1 identified, got %d trusted + %d identified", trustedCount, identifiedCount)
	}
}

func TestLoadAuthorizedKeys_EmptyPath(t *testing.T) {
	ak, err := LoadAuthorizedKeys("")
	if err != nil {
		t.Fatalf("empty path should return empty store, got error: %v", err)
	}
	if len(ak.entries) != 0 {
		t.Errorf("expected empty store, got %d entries", len(ak.entries))
	}
}

func TestLoadAuthorizedKeys_BadTier(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad")
	if err := os.WriteFile(path, []byte("tier=evil ssh-ed25519 AAAA x\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAuthorizedKeys(path); err == nil {
		t.Errorf("expected error for unknown tier, got nil")
	}
}

func TestClassifyKey_Nil(t *testing.T) {
	got := ClassifyKey(nil, &config.AuthConfig{}, nil)
	if got != TierAnonymous {
		t.Errorf("nil key should be anonymous, got %q", got)
	}
}
