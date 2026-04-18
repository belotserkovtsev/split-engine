# iOS SDK

The ladon engine runs on iOS via a gomobile-generated `.xcframework`
distributed as a Swift Package binary target. The expected host is a
Network Extension (`NEPacketTunnelProvider` or `NEDNSProxyProvider`); the
engine exposes a four-method surface and all other state flows through
file-based IPC in an app-group container.

## Two-repo distribution model

- **belotserkovtsev/Ladon** (this repo) — Go engine source.
  `mobile/ladon/` holds the gomobile binding. CI builds
  `Ladon.xcframework.zip` and attaches it to each GitHub release here.
- **belotserkovtsev/ladon-ios-sdk** — minimal Swift package manifest
  (`Package.swift` and nothing else). Each release commit points
  `binaryTarget.url` at the corresponding Ladon release asset. This is
  the repo SPM consumers depend on.

The split exists because SPM resolves `Package.swift` only at the root
of the repo given in `.package(url:)`, and we'd rather keep the Go
repo's root free of Swift manifests. CI maintains the SDK repo
automatically; it's never edited by hand.

## Repo layout (this repo)

```
mobile/ladon/        Go source for the gomobile binding
    bind.go          exported Engine (New, Start, Shutdown, OnDNSQuery)
    bind_test.go     in-process smoke tests
.github/workflows/ios-release.yml
                     macos-14 build of Ladon.xcframework.zip, attaches
                     it to the Ladon release, and cross-pushes the
                     matching Package.swift to belotserkovtsev/ladon-ios-sdk
```

Path mirrors `cmd/ladon`: that directory hosts the Linux server binary,
`mobile/ladon` hosts the mobile-platform "binary" (an .xcframework).
Package name `ladon` drives gomobile's Swift module naming — consumers
see `import Ladon` with `LadonNew` / `LadonEngine`, not `Ios*`.

## Release flow

Two triggers fire `ios-release.yml`, both producing the same outcome:

1. **Push a version tag** — `git tag v0.6.0 && git push --tags`. The
   in-repo Linux `release.yml` and `ios-release.yml` fire in parallel;
   both attach their artifacts to the same GH release here, keyed by
   `v0.6.0`.
2. **Dispatch from Actions UI** — `Actions → Release iOS → Run
   workflow → version: 0.6.0`. Useful for re-releases when the tag-push
   run errored out partway, or when you want to cut a release from a
   feature branch without tagging main first.

Either path runs on `macos-14`:
1. `gomobile bind -target=ios,iossimulator -o Ladon.xcframework ./mobile/ladon`
2. `zip` + `shasum -a 256`
3. Attach `Ladon.xcframework.zip` to the Ladon GH release
4. Clone `ladon-ios-sdk`, overwrite its `Package.swift` with the new URL
   and checksum, commit `release: v0.6.0`, tag `v0.6.0`, push

No commit ever lands on this repo's `main` or on feature branches as
part of a release — the release artifacts live on the release, and the
manifest lives on the SDK repo.

## One-time setup

The cross-repo push step needs a secret named `LADON_IOS_SDK_PAT` on
this repo — a personal access token with write access to
`belotserkovtsev/ladon-ios-sdk`. `GITHUB_TOKEN` is scoped to the repo
that triggered the workflow and can't push to a sibling repo.

Add it at `Settings → Secrets and variables → Actions → New repository secret`.

## App-repo integration

### Add the dependency

In your iOS app's `Package.swift` (or `File → Add Package Dependencies`
in Xcode):

```swift
.package(url: "https://github.com/belotserkovtsev/ladon-ios-sdk", from: "0.6.0"),
```

Link the `Ladon` library to your Network Extension target (and only
that target — the container app doesn't need the engine).

### Prerequisites

- Xcode 15+ (Swift 5.9+)
- iOS 16+ deployment target
- Apple Developer membership (even for self-signed dev builds,
  `NEPacketTunnelProvider` requires the `networking.networkextension`
  entitlement)
- App Group enabled on both the container app and the NE extension —
  the engine's SQLite DB and `hot-snapshot.json` live there

### Minimal NE extension skeleton

```swift
import NetworkExtension
import Ladon

class PacketTunnelProvider: NEPacketTunnelProvider {
    var engine: LadonEngine?
    var hotIPSet = Set<String>()
    var snapshotWatcher: DispatchSourceFileSystemObject?

    override func startTunnel(
        options: [String : NSObject]?,
        completionHandler: @escaping (Error?) -> Void
    ) {
        let groupURL = FileManager.default.containerURL(
            forSecurityApplicationGroupIdentifier: "group.YOUR.BUNDLE.ID"
        )!
        let dbPath = groupURL.appendingPathComponent("engine.db").path
        let snapPath = groupURL.appendingPathComponent("hot-snapshot.json").path

        let cfg = """
        {
          "db_path": "\(dbPath)",
          "snapshot_path": "\(snapPath)",
          "probe_timeout_ms": 800,
          "inline_probe_concurrency": 8
        }
        """

        do {
            engine = try LadonNew(cfg)
            try engine?.start()
        } catch {
            completionHandler(error); return
        }

        // Watch the snapshot file; reload the routing set on every write.
        installSnapshotWatcher(at: snapPath)

        // Configure the tunnel and proceed. Routing decisions happen in
        // handleNewFlow / packet processing using hotIPSet.
        let settings = NEPacketTunnelNetworkSettings(tunnelRemoteAddress: "10.8.0.1")
        // ... fill in NEIPv4Settings, DNS, etc. ...
        setTunnelNetworkSettings(settings) { err in completionHandler(err) }
    }

    override func stopTunnel(
        with reason: NEProviderStopReason,
        completionHandler: @escaping () -> Void
    ) {
        snapshotWatcher?.cancel()
        try? engine?.shutdown()
        completionHandler()
    }

    func onIncomingDNSQuery(domain: String, resolvedIPs: [String]) {
        let ipsJSON = (try? String(data: JSONEncoder().encode(resolvedIPs), encoding: .utf8)) ?? "[]"
        // Fire-and-forget — returns in sub-millisecond time.
        engine?.onDNSQuery(domain, resolvedIPsJSON: ipsJSON)
    }

    func installSnapshotWatcher(at path: String) {
        let fd = open(path, O_EVTONLY)
        guard fd >= 0 else { return }
        let source = DispatchSource.makeFileSystemObjectSource(
            fileDescriptor: fd,
            eventMask: [.write, .rename, .delete],
            queue: .global()
        )
        source.setEventHandler { [weak self] in
            self?.reloadSnapshot(from: path)
            // DispatchSource dies on rename/delete. Re-arm by reinstalling.
            let flags = source.data
            if flags.contains(.rename) || flags.contains(.delete) {
                source.cancel()
                self?.installSnapshotWatcher(at: path)
            }
        }
        source.setCancelHandler { close(fd) }
        source.resume()
        snapshotWatcher = source
        reloadSnapshot(from: path)  // initial load
    }

    func reloadSnapshot(from path: String) {
        guard let data = try? Data(contentsOf: URL(fileURLWithPath: path)) else { return }
        guard let snap = try? JSONDecoder().decode(HotSnapshot.self, from: data) else { return }
        var ips = Set<String>()
        for entry in snap.hot { ips.formUnion(entry.ips) }
        for entry in snap.cache { ips.formUnion(entry.ips) }
        hotIPSet = ips
    }
}

struct HotSnapshot: Decodable {
    let version: Int
    let generatedAt: String
    let hot: [Entry]
    let cache: [Entry]
    let manualDeny: [Entry]

    enum CodingKeys: String, CodingKey {
        case version, hot, cache
        case generatedAt = "generated_at"
        case manualDeny = "manual_deny"
    }

    struct Entry: Decodable {
        let domain: String
        let ips: [String]
    }
}
```

### Routing with `hotIPSet`

Inside `handleNewFlow` (for `NEPacketTunnelProvider`) or packet processing,
check the flow's destination IP against `hotIPSet`. Hit → route via the
outer WireGuard tunnel; miss → passthrough. The set is purely Swift-side
— no gomobile call on the packet hot path.

### DNS interception routing

**Critical:** probe sockets and the DNS upstream forwarder must bypass
the tunnel, or probe traffic is wrapped in the tunnel too and the engine
never sees the real DPI signal. Use `NWConnection` with
`requiredInterfaceType = .wifi` (or `.cellular`) and
`prohibitedInterfaceTypes = [.other]` on the sockets you originate for
probes/DNS. Validate by snooping the tunnel exit with `tcpdump` and
confirming probe traffic is absent.

## Config JSON schema

All fields are optional except `db_path`. Integer durations (not Go's
time.Duration string format) — Swift hand-rolls these without a
Duration parser.

| Field | Type | Default (engine.Defaults) | Notes |
|---|---|---|---|
| `db_path` | string | — (required) | Absolute path to SQLite file |
| `snapshot_path` | string | "" | Where to write hot-snapshot.json |
| `probe_timeout_ms` | int | 800 | Per-stage probe timeout |
| `probe_cooldown_sec` | int | 300 | Min interval between re-probes of same domain |
| `inline_probe_concurrency` | int | 8 | Max concurrent inline probes |
| `hot_ttl_sec` | int | 86400 | How long hot_entries live (24h default) |
| `dns_freshness_sec` | int | 21600 | Max dns_cache age for snapshot IP join (6h default) |
| `publish_interval_sec` | int | 10 | Safety-net publisher tick (event-driven publishes are instant) |
| `scorer_interval_sec` | int | 600 | Scorer scan cadence |
| `scorer_window_sec` | int | 86400 | Probe-history window for promotion |
| `scorer_fail_threshold` | int | 50 | Fails required to promote hot → cache |
| `remote_probe_url` | string | "" | Optional exit-compare validator URL |
| `remote_probe_timeout_ms` | int | 2000 | Remote probe HTTP timeout |
| `remote_probe_auth_header` | string | "" | e.g. "Authorization" |
| `remote_probe_auth_value` | string | "" | e.g. "Bearer xxxxx" |

## Troubleshooting

**`LadonNew` throws on start** — config JSON parse or SQLite open
failed. Check `db_path` is in the app-group container (not the
extension's private sandbox, which is cleared on each NE cold-start).

**Snapshot file never appears** — `snapshot_path` is missing from
config, or the publisher goroutine didn't spawn. Engine only starts the
publisher when `SnapshotPath != ""`.

**hotIPSet stays empty** — probe classification is silently failing,
probably because probe traffic is going through the tunnel itself. See
"DNS interception routing" above.

**SPM fails to resolve** — the tag hasn't been published to
`ladon-ios-sdk` yet. Check that `ios-release.yml` completed
successfully on the matching release; if the "Publish manifest to
ladon-ios-sdk" step failed, verify `LADON_IOS_SDK_PAT` is set and
still valid.

**Sudden NE termination / OOM** — the 50 MB NE memory cap is tight.
Check `Instruments → Allocations`. If the engine's Go heap dominates,
consider moving the engine into the container app and reaching the NE
via XPC (bigger refactor — open an issue first).
