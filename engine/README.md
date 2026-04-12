# Engine components

Planned modules:

- `watcher.py` — ingest dnsmasq events
- `prober.py` — perform DNS/TCP/TLS probes
- `decision.py` — classify domains into ignore/watch/hot
- `scorer.py` — promote stable domains into cache
- `storage.py` — SQLite access layer
