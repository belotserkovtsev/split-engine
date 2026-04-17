package storage

import (
	"context"
	"time"
)

// InsertObservedFlow appends one row to observed_flows. Caller (the
// Observer) dedupes upstream before calling here — this method is a thin
// write, no idempotency checks.
func (s *Store) InsertObservedFlow(ctx context.Context, dstIP, proto string, dstPort int, srcClient string, at time.Time) error {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO observed_flows (dst_ip, proto, dst_port, src_client, observed_at)
		 VALUES (?, ?, ?, ?, ?)`,
		dstIP, proto, dstPort, srcClient, formatTime(at))
	return err
}

// DeleteObservedFlowsBefore purges observed_flows rows older than cutoff.
// Invoked from `ladon prune -flows -before <date>`. Returns rows deleted.
func (s *Store) DeleteObservedFlowsBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM observed_flows WHERE observed_at < ?`, formatTime(cutoff))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// CountObservedFlows is a tiny helper mostly used by tests and by
// operator-facing status commands.
func (s *Store) CountObservedFlows(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM observed_flows`).Scan(&n)
	return n, err
}
