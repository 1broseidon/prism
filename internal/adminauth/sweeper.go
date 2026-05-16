package adminauth

import (
	"context"
	"encoding/json"
	"time"

	"github.com/1broseidon/prism/internal/store"
)

// sweepInterval is how often the GC goroutine wakes to purge expired records.
const sweepInterval = 15 * time.Minute

// StartSweeper launches a goroutine that periodically deletes expired sessions
// and login attempts from the KV store. The goroutine exits when ctx is
// canceled, which the caller wires to gateway shutdown.
//
// Why this exists: GetSession / TakeLoginAttempt only delete entries that are
// actually read again. Records that age out without another visit stay in
// the KV forever, slowly bloating the store. The sweeper bounds that.
func (h *Holder) StartSweeper(ctx context.Context) {
	if h == nil || h.kv == nil {
		return
	}
	go func() {
		// First sweep on a short delay so it doesn't fight startup work,
		// then on the regular interval.
		timer := time.NewTimer(30 * time.Second)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				if n, err := sweepExpired(h.kv); err != nil {
					h.logger.Warn("adminauth sweep failed", "error", err)
				} else if n > 0 {
					h.logger.Info("adminauth sweep removed expired entries", "count", n)
				}
				timer.Reset(sweepInterval)
			}
		}
	}()
}

// sweepExpired deletes session and login-attempt records whose own embedded
// expiry/age has passed. Returns the number of entries removed.
func sweepExpired(kv store.Store) (int, error) {
	now := time.Now()
	removed := 0

	sessionKeys, err := kv.List(sessionKVPrefix)
	if err != nil {
		return removed, err
	}
	for _, key := range sessionKeys {
		data, getErr := kv.Get(key)
		if getErr != nil {
			continue
		}
		var sess Session
		if json.Unmarshal(data, &sess) != nil {
			// Corrupt entry — drop it.
			_ = kv.Delete(key)
			removed++
			continue
		}
		if sess.Expired(now) {
			_ = kv.Delete(key)
			removed++
		}
	}

	loginKeys, err := kv.List(loginKVPrefix)
	if err != nil {
		return removed, err
	}
	for _, key := range loginKeys {
		data, getErr := kv.Get(key)
		if getErr != nil {
			continue
		}
		var a LoginAttempt
		if json.Unmarshal(data, &a) != nil {
			_ = kv.Delete(key)
			removed++
			continue
		}
		if now.Sub(a.CreatedAt) > loginAttemptMaxAge {
			_ = kv.Delete(key)
			removed++
		}
	}
	return removed, nil
}
