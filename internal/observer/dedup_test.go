package observer

import (
	"testing"
	"time"
)

func TestDedup_SuppressesWithinTTL(t *testing.T) {
	d := NewDedup(60 * time.Second)
	t0 := time.Unix(0, 0)

	if d.SeenRecently("k", t0) {
		t.Error("first call should not be suppressed")
	}
	if !d.SeenRecently("k", t0.Add(30*time.Second)) {
		t.Error("call within TTL should be suppressed")
	}
	if !d.SeenRecently("k", t0.Add(59*time.Second)) {
		t.Error("call within TTL should be suppressed")
	}
	if d.SeenRecently("k", t0.Add(61*time.Second)) {
		t.Error("call after TTL should not be suppressed")
	}
}

func TestDedup_DistinctKeysAreIndependent(t *testing.T) {
	d := NewDedup(60 * time.Second)
	t0 := time.Unix(0, 0)

	if d.SeenRecently("a", t0) {
		t.Error("first 'a'")
	}
	if d.SeenRecently("b", t0) {
		t.Error("first 'b'")
	}
	if !d.SeenRecently("a", t0.Add(time.Second)) {
		t.Error("second 'a' should be suppressed")
	}
}

// TestDedup_ZeroTTLDisables is the escape hatch — operator can turn off
// suppression by setting ttl to 0. Used in tests or for debugging.
func TestDedup_ZeroTTLDisables(t *testing.T) {
	d := NewDedup(0)
	t0 := time.Unix(0, 0)
	if d.SeenRecently("k", t0) || d.SeenRecently("k", t0) {
		t.Error("zero TTL should never suppress")
	}
}

func TestDedup_GCPrunesStale(t *testing.T) {
	d := NewDedup(60 * time.Second)
	t0 := time.Unix(0, 0)

	d.SeenRecently("old", t0)
	d.SeenRecently("fresh", t0.Add(70*time.Second))
	if d.Size() != 2 {
		t.Fatalf("pre-GC size = %d, want 2", d.Size())
	}

	d.GC(t0.Add(90 * time.Second)) // cutoff = 30s; "old" @ t0 is stale, "fresh" @ 70s is live
	if d.Size() != 1 {
		t.Errorf("post-GC size = %d, want 1", d.Size())
	}
}
