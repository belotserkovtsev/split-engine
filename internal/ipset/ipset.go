// Package ipset wraps the `ipset` CLI for atomic set mutations.
//
// We reach for exec here rather than netlink because (a) the CLI is already
// on the gateway for other tooling, (b) rule volume is tiny (hundreds of
// IPs, not millions), (c) pure-Go netlink deps add cross-compile pain we
// don't need. If that ever changes, swap the implementation behind Manager.
package ipset

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Manager operates on a single named set.
type Manager struct {
	Name string
}

// New returns a manager for an existing set. It does NOT create the set —
// that's an operator concern (ipset create prod hash:ip ...) so the engine
// never mutates set schema.
func New(name string) *Manager {
	return &Manager{Name: name}
}

// Exists reports whether the set is actually present on the system.
func (m *Manager) Exists(ctx context.Context) (bool, error) {
	cmd := exec.CommandContext(ctx, "ipset", "list", "-n", m.Name)
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Members returns the current IPs in the set.
func (m *Manager) Members(ctx context.Context) ([]string, error) {
	out, err := exec.CommandContext(ctx, "ipset", "list", m.Name).Output()
	if err != nil {
		return nil, fmt.Errorf("ipset list %s: %w", m.Name, err)
	}
	var ips []string
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	inMembers := false
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "Members:") {
			inMembers = true
			continue
		}
		if !inMembers {
			continue
		}
		if line == "" {
			continue
		}
		// Each member line is just the IP (hash:ip set).
		ips = append(ips, strings.TrimSpace(line))
	}
	return ips, sc.Err()
}

// Add inserts ip into the set. -exist makes it idempotent.
func (m *Manager) Add(ctx context.Context, ip string) error {
	if err := exec.CommandContext(ctx, "ipset", "add", "-exist", m.Name, ip).Run(); err != nil {
		return fmt.Errorf("ipset add %s %s: %w", m.Name, ip, err)
	}
	return nil
}

// Del removes ip from the set. -exist makes it idempotent (no error if absent).
func (m *Manager) Del(ctx context.Context, ip string) error {
	if err := exec.CommandContext(ctx, "ipset", "del", "-exist", m.Name, ip).Run(); err != nil {
		return fmt.Errorf("ipset del %s %s: %w", m.Name, ip, err)
	}
	return nil
}

// Reconcile makes the set contain exactly desired (and nothing more).
// Returns counts of adds and deletes applied.
func (m *Manager) Reconcile(ctx context.Context, desired []string) (added, removed int, err error) {
	current, err := m.Members(ctx)
	if err != nil {
		return 0, 0, err
	}
	want := make(map[string]struct{}, len(desired))
	for _, ip := range desired {
		want[ip] = struct{}{}
	}
	have := make(map[string]struct{}, len(current))
	for _, ip := range current {
		have[ip] = struct{}{}
	}

	for ip := range want {
		if _, ok := have[ip]; ok {
			continue
		}
		if err := m.Add(ctx, ip); err != nil {
			return added, removed, err
		}
		added++
	}
	for ip := range have {
		if _, ok := want[ip]; ok {
			continue
		}
		if err := m.Del(ctx, ip); err != nil {
			return added, removed, err
		}
		removed++
	}
	return added, removed, nil
}

// Save persists the current in-memory set to disk via `ipset save` so that
// netfilter-persistent can restore it on boot. The caller decides where the
// bytes land (file path) — we stream to stdout.
func (m *Manager) Save(ctx context.Context) ([]byte, error) {
	return exec.CommandContext(ctx, "ipset", "save", m.Name).Output()
}
