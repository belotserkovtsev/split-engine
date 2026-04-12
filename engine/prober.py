"""Probe skeleton for split-engine."""


def probe_domain(domain: str) -> dict:
    """Run a staged probe for a domain.

    Planned stages:
    - DNS resolve
    - TCP 443 connect
    - TLS SNI handshake
    - optional HTTP probe
    """
    return {
        "domain": domain,
        "dns_ok": None,
        "tcp_ok": None,
        "tls_ok": None,
        "http_ok": None,
        "resolved_ips": [],
        "failure_reason": None,
    }
