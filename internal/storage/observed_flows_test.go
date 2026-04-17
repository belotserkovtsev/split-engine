package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestInsertObservedFlow_RoundTrip(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "engine.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Init(ctx); err != nil {
		t.Fatal(err)
	}

	at := time.Date(2026, 4, 17, 17, 0, 0, 0, time.UTC)
	if err := s.InsertObservedFlow(ctx, "162.159.138.232", "tcp", 443, "192.168.0.53", at); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := s.InsertObservedFlow(ctx, "95.163.56.199", "udp", 3478, "192.168.0.77", at.Add(time.Second)); err != nil {
		t.Fatalf("insert: %v", err)
	}

	n, err := s.CountObservedFlows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("count = %d, want 2", n)
	}
}

func TestDeleteObservedFlowsBefore_PrunesOldOnly(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "engine.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Init(ctx); err != nil {
		t.Fatal(err)
	}

	old := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	fresh := time.Date(2026, 4, 17, 0, 0, 0, 0, time.UTC)
	s.InsertObservedFlow(ctx, "1.1.1.1", "tcp", 443, "192.168.0.53", old)
	s.InsertObservedFlow(ctx, "1.1.1.2", "tcp", 443, "192.168.0.53", fresh)

	cutoff := time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC)
	deleted, err := s.DeleteObservedFlowsBefore(ctx, cutoff)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1", deleted)
	}

	n, _ := s.CountObservedFlows(ctx)
	if n != 1 {
		t.Errorf("remaining rows = %d, want 1", n)
	}
}
