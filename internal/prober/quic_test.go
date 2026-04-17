package prober

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	quic "github.com/quic-go/quic-go"
)

// TestQUICProber_HandshakeSucceeds runs a local quic-go listener with a
// self-signed cert and verifies the prober completes the handshake.
func TestQUICProber_HandshakeSucceeds(t *testing.T) {
	listener := startTestQUICServer(t)
	defer listener.Close()

	host, port := splitHostPort(t, listener.Addr().String())

	p := NewQUIC(3 * time.Second)
	res := p.Probe(context.Background(), ProbeRequest{
		Domain: "test.example",
		IPs:    []string{host},
		Proto:  "quic",
		Port:   port,
	})

	if !res.TCPOK {
		t.Errorf("TCPOK (transport) = false, want true; reason=%q", res.FailureReason)
	}
	if !res.TLSOK {
		t.Errorf("TLSOK (crypto) = false, want true; reason=%q", res.FailureReason)
	}
	if res.FailureReason != "" {
		t.Errorf("unexpected failure reason on success path: %q", res.FailureReason)
	}
	if res.LatencyMS < 0 {
		t.Errorf("negative latency: %d", res.LatencyMS)
	}
}

// TestQUICProber_TimeoutOnNoListener probes a UDP address with nothing on
// the other end. Expect a failure reason and transport flags false.
func TestQUICProber_TimeoutOnNoListener(t *testing.T) {
	// 127.0.0.1:1 — no service expected. Short timeout so test stays fast.
	p := NewQUIC(300 * time.Millisecond)
	res := p.Probe(context.Background(), ProbeRequest{
		Domain: "example.com",
		IPs:    []string{"127.0.0.1"},
		Proto:  "quic",
		Port:   1,
	})
	if res.TCPOK || res.TLSOK {
		t.Errorf("no-listener path should leave transport flags false; got tcp=%v tls=%v", res.TCPOK, res.TLSOK)
	}
	if !strings.HasPrefix(res.FailureReason, "quic:") {
		t.Errorf("reason should be prefixed 'quic:', got %q", res.FailureReason)
	}
}

func TestQUICProber_RejectsNonQUICProto(t *testing.T) {
	p := NewQUIC(time.Second)
	res := p.Probe(context.Background(), ProbeRequest{
		Domain: "example.com",
		IPs:    []string{"127.0.0.1"},
		Proto:  "tcp+tls",
	})
	if res.TCPOK || res.TLSOK {
		t.Errorf("unsupported proto must leave transport flags false")
	}
	if !strings.Contains(res.FailureReason, "unsupported_proto") {
		t.Errorf("expected unsupported_proto, got %q", res.FailureReason)
	}
}

func TestQUICProber_Name(t *testing.T) {
	if got := NewQUIC(0).Name(); got != "quic" {
		t.Errorf("name = %q, want quic", got)
	}
}

// startTestQUICServer spins up a quic-go listener on 127.0.0.1:random port
// with a self-signed cert and an ALPN of "h3". It accepts connections in
// the background and closes them immediately — enough for a probe to
// observe a completed handshake. Shuts down when the test ends.
func startTestQUICServer(t *testing.T) *quic.Listener {
	t.Helper()
	tlsConf := generateQUICTLSConfig(t)
	listener, err := quic.ListenAddr("127.0.0.1:0", tlsConf, &quic.Config{
		HandshakeIdleTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("quic.ListenAddr: %v", err)
	}
	go func() {
		for {
			conn, err := listener.Accept(context.Background())
			if err != nil {
				return
			}
			_ = conn.CloseWithError(0, "bye")
		}
	}()
	return listener
}

func generateQUICTLSConfig(t *testing.T) *tls.Config {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test.example"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	keyBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"h3"},
	}
}

func splitHostPort(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, p, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(p)
	if err != nil {
		t.Fatal(err)
	}
	return host, port
}
