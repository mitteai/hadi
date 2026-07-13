# How hadi works

hadi is a client, not a platform. It runs on your machine or a CI runner, speaks SSH to your boxes, and leaves systemd and Caddy in charge between deploys. There is no daemon, no agent, no database, and no state anywhere except on the boxes themselves.

## The layout on a box

For a service named `api`:

```
/opt/api/bin/api              the running binary (or /opt/api/releases/<sha>/ for tarballs)
/opt/api/bin/api-<sha>        the last 5 releases, kept for rollback
/opt/api/hadi.json            deploy state: live color, sha, previous sha, who, when
/opt/api/releases.log         the append-only deploy ledger
/etc/api/env                  the service's environment (0640, owned by the service user)
/etc/systemd/system/api@.service    generated template unit; %i is the port
/etc/caddy/hadi/api.caddy     the proxy site; its upstream port is the live color
```

The main `/etc/caddy/Caddyfile` is just `import /etc/caddy/hadi/*.caddy`, so several services share a box without touching each other's config.

## Colors

A "color" is one of two identical instances of the service, distinguished only by port (say 4003 and 4004). Exactly one is live at a time; the other is stopped. Deploys alternate between them: the idle color starts on the new code while the live one serves untouched, and traffic moves only after the new one proves itself.

## The deploy lifecycle

Per box, sequentially, stopping at the first failure:

1. **lock**: take `/opt/<name>/hadi.lock` so two deploys can't interleave. Stale locks (30+ minutes) are ignored.
2. **ensure**: converge Caddy, the site config, directories, env file permissions. Idempotent, cheap when nothing changed.
3. **ship**: push the artifact (sha-tagged), configured files, the generated unit, extra units; `daemon-reload`.
4. **hook** `before_start` (each box).
5. **start** the idle color. The unit injects the color's port through `run.port_env`.
6. **verify**: poll `http://127.0.0.1:<idle-port><health>` until healthy or `ready_timeout_sec`. On failure: print the color's journal tail and last health response, stop it, exit 1. The live color was never touched.
7. **hook** `once_before_flip` (first box only). A failed migration aborts here, old version still serving.
8. **flip**: rewrite the site config's upstream port, `systemctl reload caddy`. The reload is graceful; no connection is dropped.
9. **confirm**: one request through Caddy's own listener. Verifying the color proves the service; only this proves the flip. On failure: flip back, stop the new color, exit 1.
10. **retire**: enable the new color for boot, `systemctl stop --no-block` the old one. It drains in-flight work on its own schedule, up to `stop_timeout_sec`.
11. **record**: write `hadi.json`, append the ledger, prune artifacts beyond the last 5.

Rollback is the same lifecycle with an earlier artifact. Env changes are the same lifecycle without a new artifact. One engine, three callers.

## Where truth lives

**The proxy config is the truth for which color is live.** `hadi.json` is metadata. If anything else flips traffic (a hand edit, an emergency), hadi reads reality from the Caddy site file and stays correct; the state file just loses its bookkeeping until the next deploy rewrites it.

## Generated units

hadi renders the service's systemd template from `deploy.json` at deploy time: user, port injection, hardening (`ProtectSystem=strict`, `NoNewPrivileges` unless capabilities are requested), writable paths, capabilities, cgroup delegation, timeouts. The unit file stops being something humans write or review, which kills the class of bugs where the unit in the repo and the unit on the box disagree. `run.unit_file` opts out.

## Discovery

DNS is the registry. Your infrastructure tooling publishes:

- `<name>.boxes.<zone>`: one A record per box (low TTL).
- `_hadi.<zone>`: a TXT record listing the hadi-managed service names, which feeds `hadi ls`.

hadi's entire discovery code path is one resolver call: no cloud API, no token, no cache, no inventory file. Records and boxes are declared together in your provisioning, so they can't drift apart. `hosts` in deploy.json overrides discovery; `--host` overrides everything.

## Environment

The box is the source of truth: `/etc/<name>/env`, read by the unit via `EnvironmentFile`. `hadi env` pulls, edits, and pushes it over SSH, and applies every change with the flip lifecycle, so config changes get the same health-gated safety as code. hadi refuses to ship an env containing the port variable, which would override the unit's per-color injection and break blue-green.

## TLS

Domain entries get a Caddy site with automatic HTTPS: certificates are obtained on first request and renewed by Caddy's own loop, which runs as a daemon on the box and needs nobody to remember anything. Port entries carry no TLS; the load balancer in front of them terminates it.

## Security posture

- hadi connects as root over SSH; the key is the single credential. CI holds it as one secret.
- Host keys are not verified (the same posture as the scripts hadi replaces). Fixing this properly means your provisioning exporting host keys.
- Nothing on the boxes listens for hadi. No agent means no agent CVEs, no agent upgrades, and no way to deploy without holding the key.
- `hadi update` verifies release binaries against the release's checksums before replacing itself, and nothing updates implicitly.
