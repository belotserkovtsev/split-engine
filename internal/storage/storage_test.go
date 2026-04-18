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
// SQLITE_BUSY contention work on ягода 2026-04-18. Two invariants to hold:
//   - every connection of the read pool reports busy_timeout=5000 and
//     foreign_keys=ON (the connector wrapper must apply per-conn PRAGMAs on
//     every fresh conn, not just the first).
//   - the write pool exposes exactly one connection, regardless of how many
//     goroutines call into it — this is how Store guarantees SQLITE_BUSY is
//     structurally impossible instead of merely timeout-dependent.
func TestPragmaAppliedOnEveryConnection(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Read pool: hold 5 conns concurrently so sql.DB is forced to grow the
	// pool past 1. Without holding, the pool reuses a single conn and we'd
	// only ever verify the first PRAGMA application.
	const N = 5
	readConns := make([]*sql.Conn, N)
	for i := range readConns {
		c, err := s.rdb.Conn(ctx)
		if err != nil {
			t.Fatalf("acquire read conn %d: %v", i, err)
		}
		readConns[i] = c
	}
	t.Cleanup(func() {
		for _, c := range readConns {
			c.Close()
		}
	})

	for i, c := range readConns {
		var busyTimeout int
		if err := c.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
			t.Fatalf("read conn %d: query busy_timeout: %v", i, err)
		}
		if busyTimeout != 5000 {
			t.Errorf("read conn %d: busy_timeout=%d, want 5000 — per-conn PRAGMA wrapper regression", i, busyTimeout)
		}
		var foreignKeys int
		if err := c.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
			t.Fatalf("read conn %d: query foreign_keys: %v", i, err)
		}
		if foreignKeys != 1 {
			t.Errorf("read conn %d: foreign_keys=%d, want 1", i, foreignKeys)
		}
	}

	// Write pool: must expose exactly one connection. Acquire one and hold
	// it; a second acquire with a fast-deadline context must time out,
	// proving the cap is enforced. Without the cap, SQLITE_BUSY under burst
	// traffic returns.
	wconn, err := s.wdb.Conn(ctx)
	if err != nil {
		t.Fatalf("acquire write conn: %v", err)
	}
	t.Cleanup(func() { wconn.Close() })

	var writeBusyTimeout, writeForeignKeys int
	if err := wconn.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&writeBusyTimeout); err != nil {
		t.Fatalf("write conn: query busy_timeout: %v", err)
	}
	if writeBusyTimeout != 5000 {
		t.Errorf("write conn: busy_timeout=%d, want 5000", writeBusyTimeout)
	}
	if err := wconn.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&writeForeignKeys); err != nil {
		t.Fatalf("write conn: query foreign_keys: %v", err)
	}
	if writeForeignKeys != 1 {
		t.Errorf("write conn: foreign_keys=%d, want 1", writeForeignKeys)
	}

	capCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	second, err := s.wdb.Conn(capCtx)
	if err == nil {
		second.Close()
		t.Error("write pool handed out a second connection while the first was held — SetMaxOpenConns(1) cap broken")
	}

	// journal_mode is database-level (persisted in the header). One check on
	// any pool covers all connections to the file.
	var journalMode string
	if err := s.rdb.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&journalMode); err != nil {
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
