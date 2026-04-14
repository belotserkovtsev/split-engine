package dnsmasq

import "testing"

func TestParseForwarded(t *testing.T) {
	line := "Apr 14 17:50:28 dnsmasq[854]: 159 10.10.0.2/1 forwarded api.segment.io to 1.1.1.1"
	ev, ok := Parse(line)
	if !ok {
		t.Fatal("expected parse to succeed")
	}
	if ev.Action != Forwarded || ev.Domain != "api.segment.io" || ev.Peer != "10.10.0.2" || ev.Target != "1.1.1.1" {
		t.Fatalf("unexpected event: %+v", ev)
	}
}

func TestParseReply(t *testing.T) {
	line := "Apr 14 17:50:28 dnsmasq[854]: 159 10.10.0.2/1 reply api.segment.io is 52.13.185.92"
	ev, ok := Parse(line)
	if !ok {
		t.Fatal("expected parse to succeed")
	}
	if ev.Action != Reply || ev.Target != "52.13.185.92" {
		t.Fatalf("unexpected event: %+v", ev)
	}
}

func TestParseQueryRecordType(t *testing.T) {
	line := "Apr 14 17:50:28 dnsmasq[854]: 160 10.10.0.5/2 query[AAAA] youtube.com from 10.10.0.5"
	ev, ok := Parse(line)
	if !ok {
		t.Fatal("expected parse to succeed")
	}
	if ev.Action != Query {
		t.Fatalf("want action=query, got %q", ev.Action)
	}
	if ev.RecordType != "AAAA" {
		t.Fatalf("want record_type=AAAA, got %q", ev.RecordType)
	}
	if ev.Domain != "youtube.com" {
		t.Fatalf("want domain=youtube.com, got %q", ev.Domain)
	}
}

func TestParseCached(t *testing.T) {
	line := "Apr 14 11:48:54 dnsmasq[4409]: 5 10.10.0.7/48149 cached cachefly.cachefly.net is <CNAME>"
	ev, ok := Parse(line)
	if !ok {
		t.Fatal("expected parse to succeed")
	}
	if ev.Action != Cached || ev.Domain != "cachefly.cachefly.net" || ev.Peer != "10.10.0.7" {
		t.Fatalf("unexpected event: %+v", ev)
	}
}

func TestParseIgnoresGarbage(t *testing.T) {
	if _, ok := Parse("blah blah"); ok {
		t.Fatal("expected parse to fail on garbage")
	}
}

func TestParseWithoutSyslogPrefix(t *testing.T) {
	line := "159 10.10.0.2/1 forwarded example.com to 8.8.8.8"
	ev, ok := Parse(line)
	if !ok {
		t.Fatal("expected parse to succeed without syslog prefix")
	}
	if ev.Domain != "example.com" {
		t.Fatalf("want example.com, got %s", ev.Domain)
	}
}

func TestParseLowercasesDomain(t *testing.T) {
	ev, ok := Parse("Apr 14 17:50:28 dnsmasq[854]: 1 10.10.0.2/1 forwarded Example.COM to 1.1.1.1")
	if !ok {
		t.Fatal("expected parse to succeed")
	}
	if ev.Domain != "example.com" {
		t.Fatalf("domain not lowercased: %q", ev.Domain)
	}
}
