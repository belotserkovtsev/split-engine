package observer

import (
	"context"
	"net/netip"
	"sync"
	"testing"
	"time"
)

// recorder is an in-memory FlowWriter for tests.
type recorder struct {
	mu   sync.Mutex
	rows []row
}
type row struct {
	dstIP, proto, srcClient string
	dstPort                 int
	at                      time.Time
}

func (r *recorder) InsertObservedFlow(ctx context.Context, dstIP, proto string, dstPort int, srcClient string, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rows = append(r.rows, row{dstIP, proto, srcClient, dstPort, at})
	return nil
}
func (r *recorder) Len() int { r.mu.Lock(); defer r.mu.Unlock(); return len(r.rows) }

func addr(s string) netip.Addr { a, _ := netip.ParseAddr(s); return a }

func TestObserver_WritesFlow(t *testing.T) {
	rec := &recorder{}
	o, err := New(Config{DedupTTL: 60 * time.Second}, rec)
	if err != nil {
		t.Fatal(err)
	}
	o.handle(context.Background(), Event{
		SrcClient: addr("192.168.0.53"),
		DstIP:     addr("162.159.138.232"),
		Proto:     "tcp",
		DstPort:   443,
		At:        time.Unix(0, 0),
	})
	if rec.Len() != 1 {
		t.Fatalf("rows = %d, want 1", rec.Len())
	}
}

func TestObserver_SuppressesDuplicateWithinTTL(t *testing.T) {
	rec := &recorder{}
	o, _ := New(Config{DedupTTL: 60 * time.Second}, rec)
	ev := Event{
		SrcClient: addr("192.168.0.53"),
		DstIP:     addr("162.159.138.232"),
		Proto:     "tcp",
		DstPort:   443,
		At:        time.Unix(0, 0),
	}
	o.handle(context.Background(), ev)
	ev.At = ev.At.Add(30 * time.Second)
	o.handle(context.Background(), ev)
	if rec.Len() != 1 {
		t.Errorf("rows = %d, want 1 (dup suppressed)", rec.Len())
	}
	ev.At = ev.At.Add(60 * time.Second) // total 90s from first, >60s from second
	o.handle(context.Background(), ev)
	if rec.Len() != 2 {
		t.Errorf("rows = %d, want 2 (TTL expired, re-emit)", rec.Len())
	}
}

func TestObserver_DistinctTuplesInsertSeparately(t *testing.T) {
	rec := &recorder{}
	o, _ := New(Config{DedupTTL: 60 * time.Second}, rec)
	cases := []Event{
		{SrcClient: addr("192.168.0.53"), DstIP: addr("1.1.1.1"), Proto: "tcp", DstPort: 443, At: time.Unix(0, 0)},
		{SrcClient: addr("192.168.0.77"), DstIP: addr("1.1.1.1"), Proto: "tcp", DstPort: 443, At: time.Unix(0, 0)}, // different src
		{SrcClient: addr("192.168.0.53"), DstIP: addr("1.1.1.1"), Proto: "udp", DstPort: 443, At: time.Unix(0, 0)}, // different proto
		{SrcClient: addr("192.168.0.53"), DstIP: addr("1.1.1.1"), Proto: "tcp", DstPort: 80, At: time.Unix(0, 0)},  // different port
		{SrcClient: addr("192.168.0.53"), DstIP: addr("1.1.1.2"), Proto: "tcp", DstPort: 443, At: time.Unix(0, 0)}, // different dst
	}
	for _, ev := range cases {
		o.handle(context.Background(), ev)
	}
	if rec.Len() != len(cases) {
		t.Errorf("rows = %d, want %d (all distinct tuples)", rec.Len(), len(cases))
	}
}

func TestObserver_LocalSubnetFilter(t *testing.T) {
	rec := &recorder{}
	o, err := New(Config{LocalSubnet: "192.168.0.0/24", DedupTTL: time.Minute}, rec)
	if err != nil {
		t.Fatal(err)
	}
	// In-subnet — kept
	o.handle(context.Background(), Event{
		SrcClient: addr("192.168.0.53"), DstIP: addr("1.1.1.1"),
		Proto: "tcp", DstPort: 443, At: time.Unix(0, 0),
	})
	// Out-of-subnet — filtered. This is the gateway-own-egress case:
	// ladon probes from 10.0.0.1 (wg-side address) shouldn't pollute
	// observed_flows with "client" events for ladon's own probe traffic.
	o.handle(context.Background(), Event{
		SrcClient: addr("10.0.0.1"), DstIP: addr("1.1.1.1"),
		Proto: "tcp", DstPort: 443, At: time.Unix(2, 0),
	})
	if rec.Len() != 1 {
		t.Errorf("rows = %d, want 1 (out-of-subnet src filtered)", rec.Len())
	}
}

func TestObserver_NewRejectsBadSubnet(t *testing.T) {
	_, err := New(Config{LocalSubnet: "not a cidr"}, &recorder{})
	if err == nil {
		t.Error("New should reject unparseable LocalSubnet")
	}
}
