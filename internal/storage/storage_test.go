package storage

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Init(context.Background()); err != nil {
		t.Fatalf("init: %v", err)
	}
	return s
}

// TestPragmaAppliedOnEveryConnection is the regression guard for the
// SQLITE_BUSY storm root-caused on ягода 2026-04-18: modernc.org/sqlite's
// DSN _pragma= form silently drops per-connection PRAGMAs (busy_timeout,
// foreign_keys) on every conn past the first. A single sql.DB pool that
// opens N conns therefore has one conn with pragmas set and N-1 without,
// and any write contention on the latter returns SQLITE_BUSY immediately.
//
// Open() applies the PRAGMAs via a connector wrapper, so every pooled conn
// MUST report busy_timeout=5000 and foreign_keys=1. If this ever regresses,
// the test catches it before we ship another lossy build.
func TestPragmaAppliedOnEveryConnection(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Hold several conns concurrently so the pool is forced to open fresh
	// ones — otherwise sql.DB reuses one connection and we'd only verify
	// the first pragma application.
	const N = 5
	conns := make([]*sql.Conn, N)
	for i := range conns {
		c, err := s.db.Conn(ctx)
		if err != nil {
			t.Fatalf("acquire conn %d: %v", i, err)
		}
		conns[i] = c
	}
	t.Cleanup(func() {
		for _, c := range conns {
			c.Close()
		}
	})

	for i, c := range conns {
		var busyTimeout int
		if err := c.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
			t.Fatalf("conn %d: query busy_timeout: %v", i, err)
		}
		if busyTimeout != 5000 {
			t.Errorf("conn %d: busy_timeout=%d, want 5000 — DSN _pragma regression, connector wrapper broken", i, busyTimeout)
		}
		var foreignKeys int
		if err := c.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
			t.Fatalf("conn %d: query foreign_keys: %v", i, err)
		}
		if foreignKeys != 1 {
			t.Errorf("conn %d: foreign_keys=%d, want 1", i, foreignKeys)
		}
	}

	// journal_mode is database-level (persisted in the header), so one check
	// on any conn covers all. Still worth asserting — a misconfigured DSN
	// would leave the DB in the default 'delete' mode.
	var journalMode string
	if err := s.db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("journal_mode=%q, want %q", journalMode, "wal")
	}
}

func TestUpsertDomainCreatesAndBumps(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.UpsertDomain(ctx, "example.com", "10.10.0.2", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertDomain(ctx, "example.com", "10.10.0.2", time.Time{}); err != nil {
		t.Fatal(err)
	}

	doms, err := s.ListRecentDomains(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(doms) != 1 {
		t.Fatalf("want 1 domain, got %d", len(doms))
	}
	if doms[0].HitCount != 2 {
		t.Fatalf("want hit_count=2, got %d", doms[0].HitCount)
	}
	if doms[0].State != "new" {
		t.Fatalf("want state=new, got %s", doms[0].State)
	}
}

func TestInsertProbeLinksToDomain(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.UpsertDomain(ctx, "example.com", "", time.Time{}); err != nil {
		t.Fatal(err)
	}

	ok := true
	id, err := s.InsertProbe(ctx, ProbeResult{
		Domain:      "example.com",
		DNSOK:       &ok,
		TCPOK:       &ok,
		TLSOK:       &ok,
		ResolvedIPs: []string{"93.184.216.34"},
		LatencyMS:   42,
	}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if id == 0 {
		t.Fatalf("expected non-zero probe id")
	}

	doms, err := s.ListRecentDomains(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if doms[0].LastProbeID == nil || *doms[0].LastProbeID != id {
		t.Fatalf("last_probe_id not linked: %+v", doms[0].LastProbeID)
	}
}
