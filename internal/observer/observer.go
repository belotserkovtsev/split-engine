package observer

import (
	"context"
	"fmt"
	"log"
	"net/netip"
	"time"
)

// Config knobs for the observer. All optional — defaults applied at New().
type Config struct {
	// Enabled gates the whole observer. When false, New returns a no-op
	// Observer whose Run exits immediately.
	Enabled bool

	// LocalSubnet restricts flow observation to LAN-originated traffic
	// (src IP within the given prefix). Empty = accept any src. Used to
	// exclude the gateway's own egress (including ladon's probe traffic)
	// from the observed_flows dataset.
	LocalSubnet string

	// DedupTTL is the 4-tuple suppression window. One row per
	// (src_client, dst_ip, proto, dst_port) per TTL window lands in DB.
	// Default 60s. Zero disables suppression.
	DedupTTL time.Duration

	// GCInterval is the period at which the dedup map is pruned of stale
	// entries. Default 5 minutes.
	GCInterval time.Duration
}

func (c *Config) applyDefaults() {
	if c.DedupTTL == 0 {
		c.DedupTTL = 60 * time.Second
	}
	if c.GCInterval == 0 {
		c.GCInterval = 5 * time.Minute
	}
}

// FlowWriter is the storage sink for observed flows. Kept as an interface
// so tests can inject an in-memory recorder without spinning up SQLite.
type FlowWriter interface {
	InsertObservedFlow(ctx context.Context, dstIP, proto string, dstPort int, srcClient string, at time.Time) error
}

// Observer is the platform-agnostic orchestrator. The Linux-only subscriber
// lives in conntrack_linux.go and feeds events into the Observer's pipeline.
type Observer struct {
	cfg    Config
	writer FlowWriter
	dedup  *Dedup

	subnet   netip.Prefix
	hasLocal bool
}

// Event is the platform-agnostic view of one conntrack NEW event. The
// Linux-specific subscriber translates raw netlink messages into this shape
// and hands them to Observer.handle.
type Event struct {
	SrcClient netip.Addr
	DstIP     netip.Addr
	Proto     string // "tcp" | "udp"
	DstPort   uint16
	At        time.Time
}

// New constructs an Observer. Returns an error if LocalSubnet is set but
// unparseable — config mistake worth catching at startup, not runtime.
func New(cfg Config, writer FlowWriter) (*Observer, error) {
	cfg.applyDefaults()
	o := &Observer{
		cfg:    cfg,
		writer: writer,
		dedup:  NewDedup(cfg.DedupTTL),
	}
	if cfg.LocalSubnet != "" {
		p, err := netip.ParsePrefix(cfg.LocalSubnet)
		if err != nil {
			return nil, fmt.Errorf("observer: parse LocalSubnet %q: %w", cfg.LocalSubnet, err)
		}
		o.subnet = p
		o.hasLocal = true
	}
	return o, nil
}

// handle is the event ingestion path, exported to the package for tests and
// to the platform-specific subscriber. Applies subnet filter + dedup, then
// writes to storage. Errors on write are logged, not propagated — one bad
// row shouldn't take down the observer.
func (o *Observer) handle(ctx context.Context, ev Event) {
	if o.hasLocal && !o.subnet.Contains(ev.SrcClient) {
		return
	}
	key := ev.SrcClient.String() + "|" + ev.Proto + "|" + ev.DstIP.String() + "|" + uint16Str(ev.DstPort)
	if o.dedup.SeenRecently(key, ev.At) {
		return
	}
	if err := o.writer.InsertObservedFlow(ctx, ev.DstIP.String(), ev.Proto, int(ev.DstPort), ev.SrcClient.String(), ev.At); err != nil {
		log.Printf("observer: InsertObservedFlow: %v", err)
	}
}

// runGC periodically prunes stale dedup entries so the map doesn't grow
// unbounded on long-running gateways.
func (o *Observer) runGC(ctx context.Context) {
	t := time.NewTicker(o.cfg.GCInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			o.dedup.GC(now)
		}
	}
}

// uint16Str is a tiny alloc-free uint16 → decimal string. Avoids importing
// strconv just for one call on a hot path.
func uint16Str(v uint16) string {
	if v == 0 {
		return "0"
	}
	var buf [5]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
