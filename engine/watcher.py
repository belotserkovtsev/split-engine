"""DNS watcher skeleton for split-engine."""


def ingest_dns_event(event: dict) -> dict:
    """Normalize a dnsmasq event into a domain observation."""
    return {
        "domain": event.get("domain"),
        "peer": event.get("peer"),
        "timestamp": event.get("timestamp"),
    }
