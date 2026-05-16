package admin

// Identifier hardening for the admin API.
//
// Backend IDs, agent prism IDs, group names, and similar tokens flow into:
//   • KV keys (backend/cred/<id>, backend/config/<id>, ...)
//   • HTTP paths (e.g. POST /backends/<id>, bridge POST /manage/spawn)
//   • outbound URLs (bridge /mcp/<id>)
//   • log lines and audit entries
//
// Untrusted input here can write into other key prefixes or send requests
// to unintended endpoints. The admin role is the operator's, but the
// validator still keeps blast radius small for compromised credentials and
// guards against operator-typo mistakes.

const (
	// maxIDLength caps identifier length so KV keys stay bounded.
	maxIDLength = 64
)

// isValidID reports whether s is a syntactically safe identifier:
//   - 1..64 chars
//   - ASCII alphanum, underscore, hyphen, or dot
//   - cannot start with '.' or '-' (avoids "../", "-flag" footguns)
//
// Intentionally narrower than RFC 8615 / typical "identifier" — the admin
// API generates its own IDs and only needs to validate what comes back in
// from the wire.
func isValidID(s string) bool {
	if s == "" || len(s) > maxIDLength {
		return false
	}
	if s[0] == '.' || s[0] == '-' {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z',
			c >= 'A' && c <= 'Z',
			c >= '0' && c <= '9',
			c == '_', c == '-', c == '.':
			// ok
		default:
			return false
		}
	}
	return true
}
