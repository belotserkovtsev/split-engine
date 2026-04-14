// Package dnsmasq parses dnsmasq log lines emitted with log-queries=extra.
//
// Expected line shape:
//
//	<date> <host> dnsmasq[<pid>]: <qid> <peer_ip>/<peer_id> <action> <domain> <verb> <target>
//
// Example:
//
//	Apr 14 17:50:28 dnsmasq[854]: 159 10.10.0.2/1 forwarded api.segment.io to 1.1.1.1
//	Apr 14 17:50:28 dnsmasq[854]: 159 10.10.0.2/1 reply api.segment.io is 52.13.185.92
//	Apr 14 11:48:36 dnsmasq[4409]: 1 10.10.0.2/1 query[A] graph-fallback.facebook.com from 10.10.0.2
package dnsmasq

import (
	"regexp"
	"strings"
)

// Action is the dnsmasq event verb (forwarded / reply / query / cached / config …).
// For query lines the record type is parsed out of "query[A]" into RecordType
// and Action is normalized to "query".
type Action string

const (
	Forwarded Action = "forwarded"
	Reply     Action = "reply"
	Query     Action = "query"
	Cached    Action = "cached"
	Config    Action = "config"
)

// Event is a structured dnsmasq log line.
type Event struct {
	QueryID    string
	Peer       string // client IP, e.g. "10.10.0.2". Empty for non-peer lines.
	Action     Action
	RecordType string // "A", "AAAA" — only set for Action==Query.
	Domain     string
	// Target is the right-hand side of the event:
	//   forwarded → upstream IP
	//   reply     → resolved IP, "<CNAME>", "NODATA-IPv6", etc.
	//   query     → source peer IP (redundant with Peer; ignored)
	Target string
}

var lineRe = regexp.MustCompile(
	`^\s*(\d+)\s+` + // query id
		`(?:(\d+\.\d+\.\d+\.\d+)/\d+\s+)?` + // optional "peer_ip/peer_id"
		`([A-Za-z][\w\-\[\]]*)\s+` + // action (may include [A]/[AAAA] suffix)
		`(\S+)` + // domain
		`(?:\s+(\S+)\s+(\S+))?\s*$`, // optional "<verb> <target>"
)

// actionSplitRe pulls an optional [record-type] suffix off an action token.
var actionSplitRe = regexp.MustCompile(`^([a-z]+)(?:\[([A-Za-z0-9]+)\])?$`)

// Parse extracts an Event from a raw dnsmasq line. Returns (nil, false)
// if the line doesn't match the expected shape.
func Parse(line string) (*Event, bool) {
	// Drop syslog prefix up to and including "dnsmasq[pid]:" if present.
	if i := strings.Index(line, "dnsmasq["); i >= 0 {
		if j := strings.Index(line[i:], ":"); j >= 0 {
			line = line[i+j+1:]
		}
	}

	m := lineRe.FindStringSubmatch(line)
	if m == nil {
		return nil, false
	}

	action, recType := normalizeAction(m[3])
	ev := &Event{
		QueryID:    m[1],
		Peer:       m[2],
		Action:     action,
		RecordType: recType,
		Domain:     strings.ToLower(strings.TrimRight(m[4], ".")),
	}
	if len(m) >= 7 {
		ev.Target = m[6]
	}
	return ev, true
}

func normalizeAction(raw string) (Action, string) {
	m := actionSplitRe.FindStringSubmatch(raw)
	if m == nil {
		return Action(raw), ""
	}
	return Action(m[1]), m[2]
}
