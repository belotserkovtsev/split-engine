package storage

import (
	"context"
	"time"
)

// ListQUICCandidates returns domains that are worth a QUIC probe right now.
//
// A domain is eligible when all hold:
//  1. Something on the LAN has been observed opening UDP:443 to an IP that
//     DNS cache links back to this domain — skipping domains we only know
//     about via dnsmasq log but nobody actually connects to would waste
//     bandwidth.
//  2. We haven't QUIC-probed this domain within quicCooldown — QUIC
//     reachability changes slowly; ~1h default.
//  3. The domain isn't in the manual deny list (exact-match or eTLD+1) —
//     same filter as ListProbeCandidates.
//
// Result is a plain []string; unlike ListProbeCandidates we don't need the
// full Domain row here — probe-worker just needs the name to build a
// ProbeRequest. Keeps the JOIN output slim.
func (s *Store) ListQUICCandidates(
	ctx context.Context,
	limit int,
	now time.Time,
	quicCooldown time.Duration,
) ([]string, error) {
	cutoff := formatTime(now.Add(-quicCooldown))
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT d.domain
		FROM domains d
		JOIN dns_cache dc ON dc.domain = d.domain
		JOIN observed_flows of ON of.dst_ip = dc.ip
		WHERE of.proto = 'udp'
		  AND of.dst_port = 443
		  AND d.domain NOT IN (SELECT domain FROM manual_entries WHERE list_name = 'deny')
		  AND (d.etld_plus_one IS NULL OR d.etld_plus_one = ''
		       OR d.etld_plus_one NOT IN (SELECT domain FROM manual_entries WHERE list_name = 'deny'))
		  AND NOT EXISTS (
		    SELECT 1 FROM probes p
		    WHERE p.domain = d.domain
		      AND p.proto = 'quic'
		      AND p.created_at > ?
		  )
		ORDER BY d.last_seen_at DESC
		LIMIT ?
	`, cutoff, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
