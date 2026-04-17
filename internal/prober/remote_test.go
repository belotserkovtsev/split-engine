package prober

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRemoteProber_Success(t *testing.T) {
	var got RemoteRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("content-type = %q, want application/json", ct)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(RemoteResponse{
			DNSOK: true, TCPOK: false, TLSOK: false,
			FailureReason: "tcp:i/o timeout",
			LatencyMS:     805,
			ResolvedIPs:   []string{"1.2.3.4"},
		})
	}))
	defer srv.Close()

	p := NewRemote(srv.URL, "", "", time.Second)
	res := p.Probe(context.Background(), ProbeRequest{Domain: "blocked.example.com", IPs: []string{"1.2.3.4"}})

	if got.Domain != "blocked.example.com" {
		t.Errorf("request domain = %q, want blocked.example.com", got.Domain)
	}
	if len(got.IPs) != 1 || got.IPs[0] != "1.2.3.4" {
		t.Errorf("request ips = %v, want [1.2.3.4]", got.IPs)
	}
	if got.Port != 443 {
		t.Errorf("request port = %d, want 443", got.Port)
	}
	if got.SNI != "blocked.example.com" {
		t.Errorf("request sni = %q, want blocked.example.com", got.SNI)
	}
	if !res.DNSOK {
		t.Errorf("dns_ok = false, want true")
	}
	if res.TCPOK || res.TLSOK {
		t.Errorf("tcp/tls should be false: tcp=%v tls=%v", res.TCPOK, res.TLSOK)
	}
	if res.FailureReason != "tcp:i/o timeout" {
		t.Errorf("reason = %q", res.FailureReason)
	}
	if res.LatencyMS != 805 {
		t.Errorf("latency = %d, want 805", res.LatencyMS)
	}
}

func TestRemoteProber_Auth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Errorf("Authorization = %q, want Bearer secret", got)
			http.Error(w, "bad auth", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(RemoteResponse{DNSOK: true, TCPOK: true, TLSOK: true})
	}))
	defer srv.Close()

	p := NewRemote(srv.URL, "Authorization", "Bearer secret", time.Second)
	res := p.Probe(context.Background(), ProbeRequest{Domain: "example.com"})
	if !res.TCPOK || !res.TLSOK {
		t.Errorf("auth path should have returned ok; got reason=%q", res.FailureReason)
	}
}

func TestRemoteProber_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := NewRemote(srv.URL, "", "", time.Second)
	res := p.Probe(context.Background(), ProbeRequest{Domain: "example.com"})
	if res.TCPOK || res.TLSOK {
		t.Errorf("error path should not mark tcp/tls ok")
	}
	if res.FailureReason == "" {
		t.Errorf("reason should be set on http error")
	}
}

func TestRemoteProber_TransportFailure(t *testing.T) {
	// Point at a URL no one answers on — dial will fail fast.
	p := NewRemote("http://127.0.0.1:1", "", "", 200*time.Millisecond)
	res := p.Probe(context.Background(), ProbeRequest{Domain: "example.com"})
	if res.TCPOK || res.TLSOK {
		t.Errorf("transport failure should not mark tcp/tls ok")
	}
	if res.FailureReason == "" {
		t.Errorf("reason should be set on transport failure")
	}
}

func TestRemoteProber_BadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	p := NewRemote(srv.URL, "", "", time.Second)
	res := p.Probe(context.Background(), ProbeRequest{Domain: "example.com"})
	if res.FailureReason == "" {
		t.Errorf("bad json should produce a reason")
	}
}

func TestLocalProber_DelegatesToProbeIPs(t *testing.T) {
	// Pick an unreachable IP so we exercise the ProbeIPs path without
	// requiring internet. TEST-NET-1 is reserved and silently dropped.
	p := NewLocal(200 * time.Millisecond)
	res := p.Probe(context.Background(), ProbeRequest{Domain: "example.com", IPs: []string{"192.0.2.1"}})
	if res.TCPOK {
		t.Errorf("expected tcp fail against unreachable IP, got ok")
	}
	if res.FailureReason == "" {
		t.Errorf("expected a failure reason")
	}
}

func TestLocalProber_Name(t *testing.T) {
	if got := NewLocal(0).Name(); got != "local" {
		t.Errorf("local name = %q, want local", got)
	}
	if got := NewRemote("http://x", "", "", 0).Name(); got != "remote" {
		t.Errorf("remote name = %q, want remote", got)
	}
}

func TestProbeRequest_ApplyDefaults(t *testing.T) {
	cases := []struct {
		name     string
		in       ProbeRequest
		wantProto string
		wantPort  int
		wantSNI   string
	}{
		{
			name:      "all empty → tcp+tls/443/domain",
			in:        ProbeRequest{Domain: "example.com"},
			wantProto: "tcp+tls", wantPort: 443, wantSNI: "example.com",
		},
		{
			name:      "quic proto picks 443",
			in:        ProbeRequest{Domain: "example.com", Proto: "quic"},
			wantProto: "quic", wantPort: 443, wantSNI: "example.com",
		},
		{
			name:      "stun proto picks 3478",
			in:        ProbeRequest{Domain: "example.com", Proto: "stun"},
			wantProto: "stun", wantPort: 3478, wantSNI: "example.com",
		},
		{
			name:      "explicit values preserved",
			in:        ProbeRequest{Domain: "example.com", Proto: "quic", Port: 8443, SNI: "canary"},
			wantProto: "quic", wantPort: 8443, wantSNI: "canary",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.in.ApplyDefaults()
			if got.Proto != c.wantProto || got.Port != c.wantPort || got.SNI != c.wantSNI {
				t.Errorf("ApplyDefaults = {%s,%d,%s}, want {%s,%d,%s}",
					got.Proto, got.Port, got.SNI, c.wantProto, c.wantPort, c.wantSNI)
			}
		})
	}
}

// TestLocalProber_RejectsNonTCPTLSProto confirms the interface contract:
// LocalProber returns a descriptive failure reason when asked for a
// protocol it doesn't implement, rather than silently falling through to
// TCP+TLS (which would be the wrong answer for QUIC-only upstreams).
func TestLocalProber_RejectsNonTCPTLSProto(t *testing.T) {
	p := NewLocal(200 * time.Millisecond)
	res := p.Probe(context.Background(), ProbeRequest{Domain: "example.com", Proto: "quic"})
	if res.TCPOK || res.TLSOK {
		t.Errorf("unsupported proto should not mark tcp/tls ok")
	}
	if res.FailureReason == "" || !contains(res.FailureReason, "unsupported_proto") {
		t.Errorf("expected unsupported_proto reason, got %q", res.FailureReason)
	}
}

func TestRemoteProber_RejectsNonTCPTLSProto(t *testing.T) {
	// No HTTP server — this should short-circuit before any HTTP dial.
	p := NewRemote("http://should-not-be-reached", "", "", time.Second)
	res := p.Probe(context.Background(), ProbeRequest{Domain: "example.com", Proto: "quic"})
	if res.TCPOK || res.TLSOK {
		t.Errorf("unsupported proto should not mark tcp/tls ok")
	}
	if !contains(res.FailureReason, "unsupported_proto") {
		t.Errorf("expected unsupported_proto reason, got %q", res.FailureReason)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
