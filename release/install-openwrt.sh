#!/bin/sh
# ladon installer — OpenWRT (beta).
#
# Scope, mirrored from the Debian installer: ladon's job is keeping three
# kernel ipsets populated. Wiring those ipsets into routing (firewall4 nft
# rules / iptables MARK + ip rule fwmark + custom routing table → tunnel
# interface) is the OPERATOR'S responsibility — see docs/install-openwrt.md
# for an nftables example.
#
# Re-running this script upgrades to the latest version: existing config
# files are preserved (manual-allow.txt, manual-deny.txt, config.yaml),
# binary + procd unit + extensions are replaced, ladon is restarted.
#
# Usage:
#   sh install-openwrt.sh
#
# Or:
#   wget -O- https://github.com/belotserkovtsev/ladon/releases/latest/download/install-openwrt.sh \
#     | sh
#
# Optional env:
#   TAG=v1.4.0-rc1          install a specific release tag instead of latest
#   IPSET_ENGINE=ladon_engine, IPSET_MANUAL=ladon_manual, IPSET_CIDR=ladon_cidr
#   ASSUME_YES=1            skip the dnsmasq-full swap countdown (CI/scripts)

set -eu

# --- pretty logging (ash-compatible — no printf %b on busybox) ---
if [ -t 1 ]; then
	RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
else
	RED=''; GREEN=''; YELLOW=''; NC=''
fi
log()  { printf '%s==>%s %s\n' "$GREEN" "$NC" "$*"; }
warn() { printf '%s==>%s %s\n' "$YELLOW" "$NC" "$*"; }
die()  { printf '%s==>%s %s\n' "$RED" "$NC" "$*" >&2; exit 1; }

# --- env ---
IPSET_ENGINE="${IPSET_ENGINE:-ladon_engine}"
IPSET_MANUAL="${IPSET_MANUAL:-ladon_manual}"
IPSET_CIDR="${IPSET_CIDR:-ladon_cidr}"
GH_REPO="belotserkovtsev/ladon"
ASSUME_YES="${ASSUME_YES:-0}"

# OpenWRT layout: binary under /usr/bin, ephemeral state under /var/lib
# (writable on overlayfs), config under /etc, bundled assets under /usr/share.
PREFIX_BIN=/usr/bin
PREFIX_SHARE=/usr/share/ladon
CONFIG_DIR=/etc/ladon
STATE_DIR=/var/lib/ladon/state
INITD=/etc/init.d/ladon

# --- preflight ---
[ "$(id -u)" -eq 0 ] || die "must run as root"
[ -f /etc/openwrt_release ] || die "this script is for OpenWRT only — see install.sh for Debian/Ubuntu"
command -v opkg >/dev/null || die "opkg required (you're not on OpenWRT?)"
command -v uci >/dev/null || die "uci required"

# --- detect arch via opkg print-architecture ---
# opkg lists every supported arch with a priority (e.g. arch all 1,
# arch noarch 1, arch arm_cortex-a7_neon-vfpv4 10). We pick the highest-
# priority entry and map it to our tarball naming scheme.
detect_arch() {
	opkg_arch=$(opkg print-architecture 2>/dev/null \
		| awk '$1 == "arch" { print $3, $2 }' \
		| sort -rn \
		| awk 'NR == 1 { print $2 }')
	[ -n "$opkg_arch" ] || die "couldn't read architecture from opkg print-architecture"
	case "$opkg_arch" in
		aarch64_*|arm64*)
			TARBALL_ARCH=aarch64 ;;
		arm_cortex-a*|arm_*_neon*|arm_*_vfp*|armv7*)
			TARBALL_ARCH=armv7 ;;
		mipsel_24kc|mipsel_mips32r2|mipsel_74kc)
			TARBALL_ARCH=mipsel_24kc ;;
		x86_64)
			TARBALL_ARCH=x86_64 ;;
		*)
			die "unsupported OpenWRT architecture: $opkg_arch (supported: aarch64_*, arm_cortex-*, mipsel_24kc, x86_64)" ;;
	esac
	log "OpenWRT arch: $opkg_arch → tarball: ladon-openwrt-$TARBALL_ARCH.tar.gz"
}

# --- dnsmasq-full check & swap-with-confirm ---
# OpenWRT's stock 'dnsmasq' package is built without ipset support; only
# 'dnsmasq-full' has the ipset= directive ladon relies on. If plain dnsmasq
# is installed, we swap — but DNS goes dark for a few seconds, so give the
# operator a window to abort.
ensure_dnsmasq_full() {
	if opkg list-installed 2>/dev/null | awk '{ print $1 }' | grep -qx 'dnsmasq-full'; then
		log "dnsmasq-full already installed — good"
		return 0
	fi
	if ! opkg list-installed 2>/dev/null | awk '{ print $1 }' | grep -qx 'dnsmasq'; then
		log "no dnsmasq package present — installing dnsmasq-full"
		opkg install dnsmasq-full
		return 0
	fi

	cat <<EOF

${YELLOW}====================================================================${NC}
${YELLOW}  WARNING — about to swap dnsmasq for dnsmasq-full${NC}
${YELLOW}====================================================================${NC}

ladon needs dnsmasq's ipset= directive support, which lives only in
dnsmasq-full. The plain 'dnsmasq' currently installed will be replaced.

Effects:
  * DNS resolution will be briefly unavailable (~5s) on this router
    while opkg removes the old package and installs the new one.
  * Existing /etc/config/dhcp UCI config is preserved by the swap.
  * dnsmasq-full pulls in extra deps (DNSSEC, ipset, conntrack support).
    Expect ~200KB more flash usage.

EOF
	if [ "$ASSUME_YES" = "1" ]; then
		log "ASSUME_YES=1 — skipping countdown"
	else
		printf "Press Ctrl-C in the next 10 seconds to abort, or wait to continue.\n\n"
		i=10
		while [ "$i" -gt 0 ]; do
			printf "\r  continuing in %2d seconds (Ctrl-C to abort)... " "$i"
			sleep 1
			i=$((i - 1))
		done
		printf "\r%-60s\n" " "
	fi

	log "removing dnsmasq, installing dnsmasq-full"
	# luci-app-firewall and friends depend on 'dnsmasq', and opkg refuses
	# to remove a depended-upon package without --force-removal-of-dependent-packages.
	# dnsmasq-full satisfies the same Provides, so the dependents stay happy.
	opkg remove dnsmasq --force-removal-of-dependent-packages
	opkg install dnsmasq-full
	/etc/init.d/dnsmasq restart || warn "dnsmasq restart returned non-zero — check logread"
}

# --- step 1: arch + deps ---
detect_arch
log "running opkg update"
opkg update >/dev/null
log "installing runtime deps (ipset, ca-bundles, wget-ssl)"
opkg install ipset ca-bundles wget-ssl >/dev/null
ensure_dnsmasq_full

# --- step 2: fetch release tag ---
TAG="${TAG:-}"
if [ -z "$TAG" ]; then
	log "querying latest stable release"
	# OpenWRT has jsonfilter (libubox) — cleaner than grep|cut.
	TAG=$(wget -qO- "https://api.github.com/repos/${GH_REPO}/releases/latest" \
		| jsonfilter -e '@.tag_name' 2>/dev/null || true)
	[ -n "$TAG" ] || die "couldn't determine latest tag from GitHub API"
	log "latest version: $TAG"
else
	log "using TAG override: $TAG"
fi

WORKDIR=$(mktemp -d)
trap 'rm -rf "$WORKDIR"' EXIT
cd "$WORKDIR"

URL="https://github.com/${GH_REPO}/releases/download/${TAG}/ladon-openwrt-${TARBALL_ARCH}.tar.gz"
log "downloading ${URL##*/}"
wget -q "$URL" "${URL}.sha256"
log "verifying sha256"
sha256sum -c "ladon-openwrt-${TARBALL_ARCH}.tar.gz.sha256"
tar xzf "ladon-openwrt-${TARBALL_ARCH}.tar.gz"
SRC="ladon-openwrt-${TARBALL_ARCH}-${TAG}"

# --- step 3: install files ---
log "installing files"
mkdir -p "$PREFIX_SHARE/extensions" "$STATE_DIR" "$CONFIG_DIR"
install -m 0755 "$SRC/ladon"             "$PREFIX_BIN/ladon"
install -m 0755 "$SRC/ladon.init"        "$INITD"
[ -f "$CONFIG_DIR/manual-allow.txt" ] || \
	install -m 0644 "$SRC/manual-allow.txt.example" "$CONFIG_DIR/manual-allow.txt"
[ -f "$CONFIG_DIR/manual-deny.txt" ] || \
	install -m 0644 "$SRC/manual-deny.txt.example" "$CONFIG_DIR/manual-deny.txt"
[ -f "$CONFIG_DIR/config.yaml" ] || \
	install -m 0644 "$SRC/config.yaml.openwrt.example" "$CONFIG_DIR/config.yaml"
# Extensions are bundled assets — always overwrite to pick up upstream updates.
for ext in "$SRC/extensions/"*.txt; do
	[ -f "$ext" ] && install -m 0644 "$ext" "$PREFIX_SHARE/extensions/"
done

# --- step 4: UCI patch — dnsmasq query logging ---
# Save the operator's existing values so uninstall can roll back. Empty
# (uci -q returns 1 with no output) means the field was unset, encoded as
# the '__unset' sentinel in the backup file.
BACKUP="$CONFIG_DIR/.uci-backup"
{
	echo "# ladon UCI backup — generated $(date -u +%Y-%m-%dT%H:%M:%SZ)"
	echo "# format: KEY=VALUE  ('__unset' means the field was not set)"
	for key in logqueries logfacility; do
		val=$(uci -q get "dhcp.@dnsmasq[0].$key" 2>/dev/null || true)
		[ -n "$val" ] || val="__unset"
		echo "$key=$val"
	done
} > "$BACKUP"
chmod 0600 "$BACKUP"

log "enabling dnsmasq query logging (UCI: dhcp.@dnsmasq[0])"
uci set dhcp.@dnsmasq[0].logqueries='1'
existing_logfacility=$(uci -q get dhcp.@dnsmasq[0].logfacility 2>/dev/null || true)
if [ -z "$existing_logfacility" ]; then
	uci set dhcp.@dnsmasq[0].logfacility='/var/log/dnsmasq.log'
elif [ "$existing_logfacility" != "/var/log/dnsmasq.log" ]; then
	warn "logfacility already set to '$existing_logfacility' — keeping it"
	warn "you'll need to edit $CONFIG_DIR/config.yaml so ladon tails the right file"
fi
uci commit dhcp
/etc/init.d/dnsmasq reload

# --- step 5: init db ---
log "initializing database"
"$PREFIX_BIN/ladon" -db "$STATE_DIR/engine.db" init-db >/dev/null

# --- step 6: enable + start ---
log "enabling and starting ladon"
"$INITD" enable
"$INITD" restart

sleep 2
if ! pgrep -f '^/usr/bin/ladon' >/dev/null 2>&1; then
	warn "ladon doesn't appear to be running — check: logread -e ladon"
fi

# --- post-install message ---
cat <<EOF

${GREEN}==> ladon $TAG installed (OpenWRT beta)${NC}

What's running:
  status:   /etc/init.d/ladon status; pgrep -af ladon
  logs:     logread -f -e ladon
  config:   $CONFIG_DIR/config.yaml
  ipsets:   $IPSET_ENGINE (probe-driven), $IPSET_MANUAL (dnsmasq-driven),
            $IPSET_CIDR (extension CIDR blocks, hash:net)

${YELLOW}==> ROUTING IS YOUR JOB${NC}

ladon only fills the ipsets — wiring traffic from clients through your
tunnel when the destination IP matches one of those sets is up to you.
A minimal firewall4 (nftables) wiring example, assuming your WireGuard
interface is 'wg0' and peers come in on br-lan:

  # /etc/nftables.d/30-ladon.nft  (loaded automatically by firewall4)
  table inet ladon {
    chain mangle_prerouting {
      type filter hook prerouting priority mangle; policy accept;
      iifname "br-lan" ip daddr @ladon_engine meta mark set 0x1
      iifname "br-lan" ip daddr @ladon_manual meta mark set 0x1
      iifname "br-lan" ip daddr @ladon_cidr   meta mark set 0x1
    }
  }

  ip rule add fwmark 0x1 table 100 priority 1000
  echo '100 ladon' >> /etc/iproute2/rt_tables
  ip route replace default dev wg0 table 100

(Adjust iifname/interface, mark, table number to your setup. ipset legacy
sets are visible to nftables on OpenWRT 22.03+ via the kernel shim.)

Next steps:
  1. Wire routing as above (or whatever your VPN setup needs).
  2. Add domains to $CONFIG_DIR/manual-allow.txt or manual-deny.txt
     and restart ladon: /etc/init.d/ladon restart
  3. Or enable bundled allow presets in $CONFIG_DIR/config.yaml:
       allow_extensions: [ai, twitch, tiktok]
     Full catalogue:
       https://github.com/belotserkovtsev/ladon/blob/main/docs/extensions.md

To uninstall: download and run release/uninstall-openwrt.sh from the same
release. UCI rollback is automatic via $BACKUP.
EOF
