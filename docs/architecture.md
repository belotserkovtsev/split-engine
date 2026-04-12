# Architecture

## High-level flow

`dnsmasq events -> watcher -> queue -> prober -> decision engine -> hot/cache state -> ipset builder`

## Live path

Goal: unlock blocked domains quickly.

- watcher sees a new domain from client DNS traffic
- prober runs quick checks
- if confidence is high enough, domain is added to `hot`
- `hot` entries have TTL and expire automatically

## Slow path

Goal: build a persistent cache of stable split targets.

- scorer periodically reviews domain history
- domains with repeated evidence are promoted to `cache`
- cache is used as long-term prewarmed split state

## Probe layers

1. DNS resolve
2. TCP connect to 443
3. TLS handshake with SNI
4. Optional HTTP probe for ambiguous cases

## Design principles

- fast unlock should be reversible
- persistent promotion should require repeated evidence
- noisy domains should be filtered by deny/cooldown logic
- gateway integration should stay decoupled from engine logic during early development
