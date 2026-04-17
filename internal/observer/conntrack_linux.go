//go:build linux

package observer

import (
	"context"
	"fmt"
	"log"
	"syscall"
	"time"

	"github.com/ti-mo/conntrack"
	"github.com/ti-mo/netfilter"
)

// Run subscribes to nf_conntrack NEW events via netlink, translates each
// event into an observer.Event, and dispatches to handle. Blocks until ctx
// is cancelled or the netlink connection dies.
//
// Requires CAP_NET_ADMIN. On a ladon install the service runs as root so
// this is satisfied; for non-root callers the caller must grant the cap.
func (o *Observer) Run(ctx context.Context) error {
	if !o.cfg.Enabled {
		log.Printf("observer: disabled in config")
		<-ctx.Done()
		return nil
	}

	conn, err := conntrack.Dial(nil)
	if err != nil {
		return fmt.Errorf("observer: conntrack.Dial: %w", err)
	}
	defer conn.Close()

	evCh := make(chan conntrack.Event, 1024)
	errCh, err := conn.Listen(evCh, 4, []netfilter.NetlinkGroup{netfilter.GroupCTNew})
	if err != nil {
		return fmt.Errorf("observer: conntrack.Listen: %w", err)
	}

	log.Printf("observer: listening for conntrack NEW events (subnet filter: %s, dedup TTL: %s)",
		func() string {
			if o.cfg.LocalSubnet == "" {
				return "none"
			}
			return o.cfg.LocalSubnet
		}(), o.cfg.DedupTTL)

	go o.runGC(ctx)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errCh:
			if err != nil {
				return fmt.Errorf("observer: netlink listener: %w", err)
			}
		case raw, ok := <-evCh:
			if !ok {
				return fmt.Errorf("observer: netlink event channel closed")
			}
			ev, ok := translate(raw)
			if !ok {
				continue
			}
			ev.At = time.Now().UTC()
			o.handle(ctx, ev)
		}
	}
}

// translate converts a raw conntrack event into an observer.Event.
// Returns false (skip) for events we don't care about: non-NEW types,
// non-TCP/UDP protocols, malformed flows, or IPv6 (handled separately
// in a future step if needed).
func translate(raw conntrack.Event) (Event, bool) {
	if raw.Type != conntrack.EventNew || raw.Flow == nil {
		return Event{}, false
	}
	t := raw.Flow.TupleOrig
	if !t.IP.SourceAddress.IsValid() || !t.IP.DestinationAddress.IsValid() {
		return Event{}, false
	}
	var proto string
	switch t.Proto.Protocol {
	case syscall.IPPROTO_TCP:
		proto = "tcp"
	case syscall.IPPROTO_UDP:
		proto = "udp"
	default:
		return Event{}, false
	}
	// IPv4 only for now. IPv6 can land in a later step — needs schema
	// check (observed_flows.dst_ip is TEXT, can hold any, but routing
	// integration semantics differ).
	if !t.IP.SourceAddress.Is4() {
		return Event{}, false
	}
	return Event{
		SrcClient: t.IP.SourceAddress,
		DstIP:     t.IP.DestinationAddress,
		Proto:     proto,
		DstPort:   t.Proto.DestinationPort,
	}, true
}
