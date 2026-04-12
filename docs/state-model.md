# State model

## Domain records

Each observed domain should track at least:

- `domain`
- `etld_plus_one`
- `first_seen_at`
- `last_seen_at`
- `hit_count`
- `peer_count`
- `last_peer_id` (optional)
- `state`
- `cooldown_until`
- `last_probe_id`

## Probe records

Each probe should track:

- `domain`
- `resolved_ips`
- `dns_ok`
- `tcp_ok`
- `tls_ok`
- `http_ok` (optional)
- `failure_reason`
- `latency_ms`
- `created_at`

## Score inputs

Examples:

- repeated observations
- multiple peers
- repeated direct probe failures
- repeated hot unlocks
- denylist match penalty

## Promotion path

`new -> watch -> hot -> cache`

`deny` and `manual` override all scoring.
