package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "engine.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

// TestListQUICCandidates_RequiresUDPFlowEvidence is the core contract: domains
// that LAN clients never touched via UDP:443 should NEVER be QUIC-probed,
// even if they exist in `domains` table from dnsmasq observation.
func TestListQUICCandidates_RequiresUDPFlowEvidence(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	now := time.Now().UTC()

	// Two domains ingested from dnsmasq. One has UDP flow evidence, the other
	// only shows up in dns_cache (a lurker that resolves but nobody hits).
	if err := s.UpsertDomain(ctx, "seen-via-udp.test", "", now); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertDomain(ctx, "resolved-only.test", "", now); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertDNSObservation(ctx, "seen-via-udp.test", "1.1.1.1", now); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertDNSObservation(ctx, "resolved-only.test", "2.2.2.2", now); err != nil {
		t.Fatal(err)
	}
	// UDP:443 flow recorded for the first domain's IP only.
	if err := s.InsertObservedFlow(ctx, "1.1.1.1", "udp", 443, "192.168.0.50", now); err != nil {
		t.Fatal(err)
	}

	got, err := s.ListQUICCandidates(ctx, 10, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "seen-via-udp.test" {
		t.Errorf("candidates = %v, want [seen-via-udp.test]", got)
	}
}

// TestListQUICCandidates_RespectsCooldown enforces that a recent QUIC probe
// prevents re-probing until quicCooldown has elapsed.
func TestListQUICCandidates_RespectsCooldown(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	now := time.Now().UTC()

	if err := s.UpsertDomain(ctx, "video.test", "", now); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertDNSObservation(ctx, "video.test", "3.3.3.3", now); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertObservedFlow(ctx, "3.3.3.3", "udp", 443, "192.168.0.50", now); err != nil {
		t.Fatal(err)
	}

	// Record a QUIC probe 5 minutes ago.
	ok := true
	if _, err := s.InsertProbe(ctx, ProbeResult{
		Domain: "video.test", Proto: "quic",
		DNSOK: &ok, TCPOK: &ok, TLSOK: &ok,
	}, now.Add(-5*time.Minute)); err != nil {
		t.Fatal(err)
	}

	// 1h cooldown: still suppressed.
	got, _ := s.ListQUICCandidates(ctx, 10, now, time.Hour)
	if len(got) != 0 {
		t.Errorf("within cooldown, got %v; want none", got)
	}

	// 1-minute cooldown: re-eligible (last probe was 5 min ago).
	got, _ = s.ListQUICCandidates(ctx, 10, now, time.Minute)
	if len(got) != 1 || got[0] != "video.test" {
		t.Errorf("after cooldown, got %v; want [video.test]", got)
	}
}

// TestListQUICCandidates_SkipsDenied mirrors ListProbeCandidates' deny filter.
func TestListQUICCandidates_SkipsDenied(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	now := time.Now().UTC()

	if err := s.UpsertDomain(ctx, "denied.test", "", now); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertDNSObservation(ctx, "denied.test", "4.4.4.4", now); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertObservedFlow(ctx, "4.4.4.4", "udp", 443, "192.168.0.50", now); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertManual(ctx, "denied.test", "deny"); err != nil {
		t.Fatal(err)
	}

	got, _ := s.ListQUICCandidates(ctx, 10, now, time.Hour)
	if len(got) != 0 {
		t.Errorf("denied domain should not appear, got %v", got)
	}
}
