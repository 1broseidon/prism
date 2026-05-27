package identity

import (
	"crypto/rand"
	"regexp"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

// monotonicEntropyMu guards monotonicEntropy so concurrent goroutines
// minting ULIDs share the same entropy source. ulid.MonotonicEntropy is
// stateful (it remembers the last timestamp + entropy so successive
// ULIDs in the same millisecond stay sorted) and is not safe for
// concurrent use on its own.
var (
	monotonicEntropyMu sync.Mutex
	monotonicEntropy   = ulid.Monotonic(rand.Reader, 0)
)

// newULID returns a freshly-minted ULID as a 26-char Crockford base32
// string. The string form is the canonical wire/storage representation
// throughout Prism; the underlying [16]byte is never surfaced.
func newULID(now time.Time) string {
	monotonicEntropyMu.Lock()
	defer monotonicEntropyMu.Unlock()
	id := ulid.MustNew(ulid.Timestamp(now), monotonicEntropy)
	return id.String()
}

// ulidRe matches the 26-character Crockford base32 ULID encoding
// (uppercase). Used by callers that need to distinguish a ULID-shaped
// identifier from a display-name-shaped one in mixed-input contexts
// (e.g. compat-shim URL routes).
var ulidRe = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)

// IsULID reports whether s is the canonical 26-character Crockford
// base32 ULID encoding. It does not perform a full parse — it only
// confirms the alphabet/length — which is enough for routing decisions
// (e.g. "treat ULID-shaped path segments as IDs, otherwise resolve as
// display name").
func IsULID(s string) bool {
	return ulidRe.MatchString(s)
}
