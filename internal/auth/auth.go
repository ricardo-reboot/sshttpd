package auth

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/bugscave/sshttpd/internal/config"
	"golang.org/x/crypto/ssh"
)

// Tiers
const (
	TierAnonymous  = "anonymous"
	TierIdentified = "identified"
	TierTrusted    = "trusted"
)

// AuthorizedKeys is a parsed authorized_keys file used for tier classification.
//
// Format: one entry per line; same as OpenSSH's authorized_keys but with an
// optional `tier=trusted` or `tier=identified` keyword in the options field.
// Lines starting with `#` are comments. Default tier is `identified`.
//
// Example:
//
//	tier=trusted ssh-ed25519 AAAAC3Nz... admin@example
//	ssh-ed25519 AAAAC3Nz... regular-user
type AuthorizedKeys struct {
	mu      sync.RWMutex
	entries map[string]string // fingerprint -> tier
}

// LoadAuthorizedKeys reads an authorized_keys file and indexes entries by
// SHA256 fingerprint. Empty path returns an empty store (every presented key
// will fall through to the default tier).
func LoadAuthorizedKeys(path string) (*AuthorizedKeys, error) {
	ak := &AuthorizedKeys{entries: map[string]string{}}
	if path == "" {
		return ak, nil
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening authorized keys %s: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		tier := TierIdentified
		// Detect a leading `tier=...` token before the SSH key.
		if strings.HasPrefix(line, "tier=") {
			fields := strings.SplitN(line, " ", 2)
			if len(fields) != 2 {
				return nil, fmt.Errorf("authorized keys line %d: tier= without key", lineNo)
			}
			tierField := strings.TrimPrefix(fields[0], "tier=")
			switch tierField {
			case TierIdentified, TierTrusted:
				tier = tierField
			default:
				return nil, fmt.Errorf("authorized keys line %d: unknown tier %q", lineNo, tierField)
			}
			line = fields[1]
		}

		key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
		if err != nil {
			return nil, fmt.Errorf("authorized keys line %d: %w", lineNo, err)
		}
		fp := ssh.FingerprintSHA256(key)
		ak.entries[fp] = tier
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading authorized keys: %w", err)
	}
	return ak, nil
}

// Tier returns the tier configured for the given key, or empty string if the
// key is not listed.
func (a *AuthorizedKeys) Tier(key ssh.PublicKey) string {
	if a == nil || key == nil {
		return ""
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.entries[ssh.FingerprintSHA256(key)]
}

// ClassifyKey determines the authentication tier for a given public key.
//
// When ak is non-nil and contains the key's fingerprint, the file's tier is
// used. Otherwise, any presented key falls back to TierIdentified, and a
// missing key is TierAnonymous.
func ClassifyKey(key ssh.PublicKey, authCfg *config.AuthConfig, ak *AuthorizedKeys) string {
	if key == nil {
		return TierAnonymous
	}
	if t := ak.Tier(key); t != "" {
		return t
	}
	// Default for any key not in the authorized_keys file.
	_ = authCfg
	return TierIdentified
}

// IsCommandAllowed checks whether the given tier can execute the command.
//
// Commands may be referenced by name only ("receive-pack") or qualified
// ("api-call GET /api/items"). An entry in the allowed list matches when:
//   - it equals the full command, OR
//   - it equals the command's first token (the verb), OR
//   - it equals "<verb> <method>" for two-token entries like "api-call GET", OR
//   - it ends in "*" and the command starts with the prefix (e.g. "admin-*").
func IsCommandAllowed(tier string, command string, authCfg *config.AuthConfig) bool {
	var allowed []string
	switch tier {
	case TierAnonymous:
		allowed = append(allowed, authCfg.Anonymous...)
	case TierIdentified:
		allowed = append(allowed, authCfg.Anonymous...)
		allowed = append(allowed, authCfg.Identified...)
	case TierTrusted:
		allowed = append(allowed, authCfg.Anonymous...)
		allowed = append(allowed, authCfg.Identified...)
		allowed = append(allowed, authCfg.Trusted...)
	default:
		return false
	}

	cmdParts := splitCommand(command)
	verb := cmdParts[0]

	for _, entry := range allowed {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		// Wildcard suffix: "admin-*"
		if strings.HasSuffix(entry, "*") {
			prefix := strings.TrimSuffix(entry, "*")
			if strings.HasPrefix(verb, prefix) {
				return true
			}
			continue
		}

		entryParts := splitCommand(entry)

		// Verb-only entry like "receive-pack" matches any qualifier.
		if len(entryParts) == 1 && entryParts[0] == verb {
			return true
		}

		// Multi-part entry: every part must be a prefix of the corresponding command part.
		// "api-call GET" matches command "api-call GET /api/items".
		if len(entryParts) <= len(cmdParts) {
			match := true
			for i, p := range entryParts {
				if p != cmdParts[i] {
					match = false
					break
				}
			}
			if match {
				return true
			}
		}
	}
	return false
}

func splitCommand(s string) []string {
	parts := strings.Fields(s)
	if len(parts) == 0 {
		return []string{""}
	}
	return parts
}

// Fingerprint returns the SHA256 fingerprint of a public key.
func Fingerprint(key ssh.PublicKey) string {
	return ssh.FingerprintSHA256(key)
}
