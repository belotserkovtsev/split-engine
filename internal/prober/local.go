package prober

import (
	"context"
	"time"
)

// ProbeRequest describes what the prober should test. Fields beyond Domain
// and IPs are forward-looking for v1.0's multi-protocol pipeline: Proto
// selects transport (default "tcp+tls"), Port and SNI allow non-default
// values for protocols where they matter. In pre-QUIC ladon LocalProber and
// RemoteProber only honor Domain and IPs; Proto != "tcp+tls" is rejected.
type ProbeRequest struct {
	Domain string
	IPs    []string

	// Proto selects the transport/protocol to probe. Empty string is treated
	// as "tcp+tls" (the only protocol ladon supports pre-v1.0). Future values:
	// "quic" (HTTP/3 on UDP:443), "stun" (WebRTC on UDP:3478).
	Proto string

	// Port overrides the default for the chosen protocol. Zero falls through
	// to the protocol default: 443 for tcp+tls and quic, 3478 for stun.
	Port int

	// SNI overrides the TLS ServerName in ClientHello. Empty = Domain.
	// Used by control probes (benign-SNI, no-SNI) and by future protocols.
	SNI string
}

// ApplyDefaults returns a copy of the request with missing fields filled in
// from the protocol's standard defaults. Useful when an impl wants to read
// a well-formed request without mutating the caller's struct.
func (r ProbeRequest) ApplyDefaults() ProbeRequest {
	if r.Proto == "" {
		r.Proto = "tcp+tls"
	}
	if r.Port == 0 {
		switch r.Proto {
		case "stun":
			r.Port = 3478
		default:
			r.Port = 443
		}
	}
	if r.SNI == "" {
		r.SNI = r.Domain
	}
	return r
}

// Prober is the interface the engine uses to decide whether a domain is
// reachable. Implementations can probe locally (TCP/TLS from this host),
// defer to a remote service (RemoteProber), or specialize by protocol
// (upcoming QUICProber, STUNProber in v1.0).
type Prober interface {
	// Probe classifies the request. Implementations should honor req.Proto
	// and return a descriptive failure result when they don't support it
	// (rather than silently falling through to the default transport).
	Probe(ctx context.Context, req ProbeRequest) Result

	// Name identifies the backend in logs and metrics.
	Name() string
}

// LocalProber runs the built-in TCP:443 + TLS-SNI probe from the current host.
// It's what ladon has shipped with since v0.1.0; the type wraps the existing
// package-level ProbeIPs/Probe helpers so the engine can accept a Prober
// interface. Supports Proto="tcp+tls" (or empty, which defaults to it).
type LocalProber struct {
	Timeout time.Duration
}

// NewLocal returns a LocalProber. A zero timeout falls back to DefaultTimeout.
func NewLocal(timeout time.Duration) *LocalProber {
	return &LocalProber{Timeout: timeout}
}

// Name implements Prober.
func (p *LocalProber) Name() string { return "local" }

// Probe implements Prober. For req.Proto="tcp+tls" (or empty), runs the
// classic gateway-side TCP+TLS probe; when ips are provided it skips DNS
// and probes them directly (keeps the engine's view consistent with what
// dnsmasq gave the client). Non-tcp+tls protocols return a failure result
// describing the unsupported transport — the engine treats this like any
// other hot/ignore signal.
func (p *LocalProber) Probe(ctx context.Context, req ProbeRequest) Result {
	req = req.ApplyDefaults()
	if req.Proto != "tcp+tls" {
		return Result{
			Domain:        req.Domain,
			ResolvedIPs:   req.IPs,
			FailureReason: "local:unsupported_proto:" + req.Proto,
		}
	}
	if len(req.IPs) > 0 {
		return ProbeIPs(ctx, req.Domain, req.IPs, p.Timeout)
	}
	return Probe(ctx, req.Domain, p.Timeout)
}
