#!/usr/bin/env bash
# ladon uninstaller — undoes everything install.sh did.
#
# Usage:
#   PEER_SUBNET=10.10.0.0/16 sudo bash uninstall.sh
#
# PEER_SUBNET must match what install.sh was called with — that's the only
# reliable way to know which iptables rules to remove. Other env vars are
# read from the same defaults as install.sh.

set -euo pipefail

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
log()  { printf "%b==>%b %s\n" "$GREEN" "$NC" "$*"; }
warn() { printf "%b==>%b %s\n" "$YELLOW" "$NC" "$*"; }
die()  { printf "%b==>%b %s\n" "$RED" "$NC" "$*" >&2; exit 1; }

PEER_SUBNET="${PEER_SUBNET:-}"
FWMARK="${FWMARK:-0x1}"
IPSET_ENGINE="${IPSET_ENGINE:-ladon_engine}"
IPSET_MANUAL="${IPSET_MANUAL:-ladon_manual}"
WG_ROUTE_CHAIN="${WG_ROUTE_CHAIN:-WG_ROUTE}"
LADON_PREFIX="${LADON_PREFIX:-/opt/ladon}"
LADON_CONFIG_DIR="${LADON_CONFIG_DIR:-/etc/ladon}"

if [[ -z "$PEER_SUBNET" ]]; then
  die "PEER_SUBNET is required (must match what install.sh used)"
fi
[[ $EUID -eq 0 ]] || die "must run as root (sudo)"

log "stopping ladon"
systemctl disable --now ladon 2>/dev/null || true
rm -f /etc/systemd/system/ladon.service

log "removing dnsmasq drop-in"
rm -f /etc/systemd/system/dnsmasq.service.d/ladon-ipset.conf
rmdir /etc/systemd/system/dnsmasq.service.d 2>/dev/null || true

log "removing iptables rules"
for set in "$IPSET_ENGINE" "$IPSET_MANUAL"; do
  while iptables -t mangle -C "$WG_ROUTE_CHAIN" -s "$PEER_SUBNET" \
      -m set --match-set "$set" dst -j MARK --set-mark "$FWMARK" 2>/dev/null; do
    iptables -t mangle -D "$WG_ROUTE_CHAIN" -s "$PEER_SUBNET" \
      -m set --match-set "$set" dst -j MARK --set-mark "$FWMARK"
  done
done

log "destroying ipsets"
ipset destroy "$IPSET_ENGINE" 2>/dev/null || true
ipset destroy "$IPSET_MANUAL" 2>/dev/null || true

log "persisting netfilter state"
mkdir -p /etc/iptables
iptables-save > /etc/iptables/rules.v4
ipset save    > /etc/iptables/ipsets 2>/dev/null || true

log "removing ladon-manual.conf"
rm -f /etc/dnsmasq.d/ladon-manual.conf

log "removing $LADON_PREFIX and $LADON_CONFIG_DIR"
rm -rf "$LADON_PREFIX" "$LADON_CONFIG_DIR"

log "reloading systemd, restarting dnsmasq"
systemctl daemon-reload
systemctl restart dnsmasq 2>/dev/null || true

cat <<EOF

${GREEN}==> ladon uninstalled${NC}

What was NOT removed (tweak by hand if you want):
  - $WG_ROUTE_CHAIN chain itself (you may have other rules there)
  - dnsmasq + ipset + iptables-persistent packages (still installed)
EOF
