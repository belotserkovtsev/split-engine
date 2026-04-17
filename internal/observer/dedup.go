// Package observer subscribes to nf_conntrack events and records actual
// LAN-client connection attempts into storage. Provides connect-side evidence
// to complement ladon's dnsmasq log (resolve-side evidence).
package observer

import (
	"sync"
	"time"
)

// Dedup suppresses duplicate observed_flows inserts for flows seen within TTL.
// Key is the 4-tuple src_client/dst_ip/proto/dst_port (source port is
// deliberately excluded — ephemeral, doesn't change "what destination the
// client talks to"). Storing one row per TTL window instead of per-packet
// bounds storage growth on high-traffic gateways without losing the
// "client used this destination" signal.
type Dedup struct {
	mu   sync.Mutex
	seen map[string]time.Time
	ttl  time.Duration
}

// NewDedup returns a Dedup with the given TTL. ttl<=0 disables suppression.
func NewDedup(ttl time.Duration) *Dedup {
	return &Dedup{
		seen: make(map[string]time.Time),
		ttl:  ttl,
	}
}

// SeenRecently returns true if key was recorded within ttl — on true the
// caller should skip the write. Fixed-window semantic: timestamp updates
// only on the first call of a window (return false) and on re-entry after
// TTL expires. Suppressed calls do NOT reset the window.
//
// Net effect on a hot flow that pings every second: one row per ttl window
// lands in DB, not one row per ttl-silence-gap. Preserves temporal density
// of the "client is using this destination" signal.
func (d *Dedup) SeenRecently(key string, now time.Time) bool {
	if d.ttl <= 0 {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	last, ok := d.seen[key]
	if ok && now.Sub(last) < d.ttl {
		return true
	}
	d.seen[key] = now
	return false
}

// GC removes entries older than ttl so the map does not grow unbounded.
// Call periodically from a background goroutine.
func (d *Dedup) GC(now time.Time) {
	if d.ttl <= 0 {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	cutoff := now.Add(-d.ttl)
	for k, t := range d.seen {
		if t.Before(cutoff) {
			delete(d.seen, k)
		}
	}
}

// Size returns the current number of tracked keys. For tests/metrics.
func (d *Dedup) Size() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.seen)
}
