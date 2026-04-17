//go:build !linux

package observer

import (
	"context"
	"log"
)

// Run is a no-op on non-Linux platforms. nf_conntrack + netlink are
// Linux-kernel APIs; ladon's observer has no meaningful behavior elsewhere.
// We keep the package compilable so unit tests for dedup and handle logic
// run on any dev machine (Windows/macOS).
func (o *Observer) Run(ctx context.Context) error {
	if o.cfg.Enabled {
		log.Printf("observer: nf_conntrack subscription not supported on this platform; observer disabled")
	}
	<-ctx.Done()
	return ctx.Err()
}
