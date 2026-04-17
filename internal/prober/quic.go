package prober

import (
	"context"
	"crypto/tls"
	"net"
	"strconv"
	"time"

	quic "github.com/quic-go/quic-go"
)

// QUICProber performs an HTTP/3-shaped QUIC handshake against the target. It
// exists to close ladon's UDP blind spot: many services (Cloudflare/Google/
// Meta/Akamai) run HTTP/3 on UDP:443 alongside HTTPS on TCP:443 at the same
// IPs, and RU-style DPI sometimes filters the UDP path while leaving TCP
// untouched (or vice-versa). TCP-only probe misses this.
//
// The probe tries to complete a QUIC handshake with ALPN "h3". Outcomes:
//   - handshake completes       → Result with TCPOK+TLSOK both true
//     (reusing those booleans as "transport ok"/"crypto ok" — the Result
//     schema predates multi-protocol by a while; decision.Classify treats
//     them transport-agnostically)
//   - timeout / connection drop → Result with failure reason "quic:..."
//     and transport flags false. Engine classifies as Hot.
//   - target not speaking h3    → timeout with "no response". We can't
//     distinguish that from active DPI drop from inside ladon; downstream
//     decision logic (step 6) will combine QUIC fail with observed-UDP
//     evidence before promoting/demoting.
//
// Certificate validation is intentionally disabled (InsecureSkipVerify):
// we're testing reachability, not authenticity, and many edge POPs present
// certs that don't chain for "unknown SNI" handshakes.
type QUICProber struct {
	Timeout time.Duration
}

// NewQUIC returns a QUICProber. A zero timeout falls back to 2 seconds —
// UDP datagram loss + full QUIC handshake needs more headroom than the
// 800ms default for TCP probes.
func NewQUIC(timeout time.Duration) *QUICProber {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	return &QUICProber{Timeout: timeout}
}

// Name implements Prober.
func (p *QUICProber) Name() string { return "quic" }

// Probe implements Prober. Honors Proto="quic" (rejects others with a
// descriptive unsupported_proto failure), takes req.IPs if pre-resolved
// otherwise resolves via the system resolver, and attempts one handshake
// against the first IP. Multi-IP fanout is left for a later tuning step —
// DPI blocking is usually IP-homogeneous per destination.
func (p *QUICProber) Probe(ctx context.Context, req ProbeRequest) Result {
	req = req.ApplyDefaults()
	if req.Proto != "quic" {
		return Result{
			Domain:        req.Domain,
			ResolvedIPs:   req.IPs,
			FailureReason: "quic:unsupported_proto:" + req.Proto,
		}
	}
	started := time.Now()

	ips := req.IPs
	if len(ips) == 0 {
		resolved, err := net.DefaultResolver.LookupHost(ctx, req.Domain)
		if err != nil {
			return Result{
				Domain:        req.Domain,
				FailureReason: "quic:dns:" + err.Error(),
				LatencyMS:     latencyMS(started),
			}
		}
		ips = resolved
	}
	if len(ips) == 0 {
		return Result{
			Domain:        req.Domain,
			FailureReason: "quic:dns:no addresses",
			LatencyMS:     latencyMS(started),
		}
	}
	dnsOK := true

	addr := net.JoinHostPort(ips[0], strconv.Itoa(req.Port))
	tlsConf := &tls.Config{
		ServerName:         req.SNI,
		NextProtos:         []string{"h3"},
		InsecureSkipVerify: true,
	}

	dialCtx, cancel := context.WithTimeout(ctx, p.Timeout)
	defer cancel()

	conn, err := quic.DialAddr(dialCtx, addr, tlsConf, &quic.Config{
		HandshakeIdleTimeout: p.Timeout,
	})
	if err != nil {
		return Result{
			Domain:        req.Domain,
			DNSOK:         dnsOK,
			ResolvedIPs:   ips,
			FailureReason: "quic:" + err.Error(),
			LatencyMS:     latencyMS(started),
		}
	}
	// Handshake completed. Close cleanly so the server side sees a normal
	// application_close rather than an idle timeout. Error code 0 = no error.
	_ = conn.CloseWithError(quic.ApplicationErrorCode(0), "probe complete")

	return Result{
		Domain:      req.Domain,
		DNSOK:       dnsOK,
		TCPOK:       true, // "transport ok" — UDP datagrams flowed + handshake confirmed
		TLSOK:       true, // "crypto ok"    — QUIC handshake is TLS 1.3 under the hood
		ResolvedIPs: ips,
		LatencyMS:   latencyMS(started),
	}
}

func latencyMS(started time.Time) int {
	return int(time.Since(started) / time.Millisecond)
}
