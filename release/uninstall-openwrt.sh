#!/bin/sh
# ladon uninstaller — OpenWRT.
#
# Mirrors install-openwrt.sh: removes binary + procd unit + state, restores
# /etc/config/dhcp UCI values from the backup install-openwrt.sh saved,
# destroys ipsets (unless still referenced by firewall rules).
#
# Does NOT roll back the dnsmasq → dnsmasq-full swap. That package change
# is now load-bearing for whoever else might be using ipset= directives on
# this router; reversing it would be its own opt-in step.
#
# Routing rules (firewall4 / nft) are NOT touched — install-openwrt.sh
# didn't add them either. If you wired the ipsets into your firewall,
# remove those rules manually.
#
# Usage:
#   sh uninstall-openwrt.sh

set -eu

if [ -t 1 ]; then
	RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
else
	RED=''; GREEN=''; YELLOW=''; NC=''
fi
log()  { printf '%s==>%s %s\n' "$GREEN" "$NC" "$*"; }
warn() { printf '%s==>%s %s\n' "$YELLOW" "$NC" "$*"; }
die()  { printf '%s==>%s %s\n' "$RED" "$NC" "$*" >&2; exit 1; }

IPSET_ENGINE="${IPSET_ENGINE:-ladon_engine}"
IPSET_MANUAL="${IPSET_MANUAL:-ladon_manual}"
IPSET_CIDR="${IPSET_CIDR:-ladon_cidr}"

PREFIX_BIN=/usr/bin
PREFIX_SHARE=/usr/share/ladon
CONFIG_DIR=/etc/ladon
STATE_DIR=/var/lib/ladon
INITD=/etc/init.d/ladon

[ "$(id -u)" -eq 0 ] || die "must run as root"
[ -f /etc/openwrt_release ] || die "OpenWRT only"

log "stopping and disabling ladon"
if [ -x "$INITD" ]; then
	"$INITD" stop 2>/dev/null || true
	"$INITD" disable 2>/dev/null || true
fi
rm -f "$INITD"

log "destroying ipsets (will warn if still referenced by firewall)"
for set in "$IPSET_ENGINE" "$IPSET_MANUAL" "$IPSET_CIDR"; do
	if ipset list "$set" -t >/dev/null 2>&1; then
		ipset destroy "$set" 2>/dev/null || \
			warn "$set still in use — remove referencing firewall rules first"
	fi
done

log "removing ladon-manual.conf dnsmasq snippet"
rm -f /tmp/dnsmasq.d/ladon-manual.conf /etc/dnsmasq.d/ladon-manual.conf

# --- restore UCI from backup ---
BACKUP="$CONFIG_DIR/.uci-backup"
if [ -f "$BACKUP" ]; then
	log "restoring /etc/config/dhcp from $BACKUP"
	while IFS='=' read -r key val; do
		case "$key" in
			'#'*|'') continue ;;
		esac
		if [ "$val" = "__unset" ]; then
			uci -q delete "dhcp.@dnsmasq[0].$key" || true
		else
			uci set "dhcp.@dnsmasq[0].$key=$val"
		fi
	done < "$BACKUP"
	uci commit dhcp
	/etc/init.d/dnsmasq reload
else
	warn "no UCI backup at $BACKUP — leaving /etc/config/dhcp as-is"
	warn "you may want to remove dhcp.@dnsmasq[0].logqueries / .logfacility manually"
fi

log "removing $PREFIX_BIN/ladon, $PREFIX_SHARE, $CONFIG_DIR, $STATE_DIR"
rm -f "$PREFIX_BIN/ladon"
rm -rf "$PREFIX_SHARE" "$CONFIG_DIR" "$STATE_DIR"

cat <<EOF

${GREEN}==> ladon uninstalled${NC}

What was NOT removed (do this by hand if needed):
  - firewall4 / nftables / iptables rules YOU added that reference
    $IPSET_ENGINE / $IPSET_MANUAL / $IPSET_CIDR
  - any ip rule fwmark / routing tables you set up for the tunnel
  - the dnsmasq → dnsmasq-full package swap (now baseline on this router)
  - /var/log/dnsmasq.log (truncate or remove if you want)
EOF
