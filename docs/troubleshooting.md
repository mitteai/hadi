# Troubleshooting

First moves, whatever the problem:

```bash
hadi status -s <service>       # live color, sha, health, right now
hadi logs -s <service> -n 50   # what the service said
hadi exec -s <service> 'journalctl -u caddy -n 20 --no-pager'   # what the proxy said
```

And remember the safety property while you debug: a failed deploy, rollback, or env change leaves the previous version serving. Failures cost you time, not uptime.

## "new color never went ready"

The most common failure. hadi already printed the evidence: the last health response and the journal tail of the failed instance. Read those first; the cause is usually right there.

- **Missing or wrong env value** (journal shows a config error at boot): `hadi env edit -s <service>`, fix, redeploy.
- **A dependency is down** (health response shows a failing check): fix the dependency; the health endpoint told you which.
- **Slow boot, not broken boot** (journal looks fine, it just wasn't ready in time): raise `run.ready_timeout_sec` in deploy.json.
- Need more than 5 journal lines:

```bash
hadi logs -s <service> --unit <service>@<idle-port> -n 200
```

## "front-door check failed after flip; flipped back"

The new version was healthy but a request through the proxy failed, so hadi restored the old upstream. Almost always the proxy layer:

```bash
hadi exec -s <service> 'caddy validate --config /etc/caddy/Caddyfile 2>&1 | tail -5'
hadi exec -s <service> 'journalctl -u caddy -n 20 --no-pager'
```

## Discovery: "<name>.boxes.<zone> did not resolve"

- Record never published: `dig +short <name>.boxes.<zone> @1.1.1.1`. Empty means publish it (see terraform.md) or set `"hosts"`.
- Published but your machine says no while `@1.1.1.1` says yes: your local resolver negative-cached the name from before it existed. It expires on its own (up to the zone's negative TTL); meanwhile `--host <ip>` bypasses DNS entirely.

## SSH failures

- `ssh <box>: unable to authenticate`: wrong key. Check what hadi is using: `--ssh-key` beats `HADI_SSH_KEY` beats `~/.ssh/id_ed25519`. In CI, confirm the secret holds the full PEM including header lines.
- Connection timeout: port 22 closed to wherever you're running from, or the box is down. `hadi status --host <ip>` against another box isolates which.
- `user <name> missing`: provisioning hasn't created the service user on that box. That's deliberate; see requirements.md.

## "another hadi holds the deploy lock"

A deploy is genuinely running (CI, a colleague), or one died mid-flight. The message shows the holder and timestamp; locks older than 30 minutes are ignored automatically. If it's provably dead sooner:

```bash
hadi exec -s <service> 'rm -f /opt/<service>/hadi.lock'
```

## "refusing to ship an env that sets <PORT_VAR>"

Working as intended: the unit injects the per-color port, and an env-file value would override it and quietly break blue-green. Remove that line from your env; the service gets its port from the unit.

## "WARNING: env differs across boxes"

Someone edited one box by hand. Decide which version is right, then realign every box with it:

```bash
hadi env pull -s <service> > current.env    # pulls from the first box; inspect it
hadi env push -s <service> current.env
```

## status says UNHEALTHY but users see no errors

The health endpoint is failing on the live color while past requests still succeed. Usually a dependency check inside `/healthz` (a sidecar, disk headroom) went red before user traffic noticed:

```bash
hadi exec -s <service> 'curl -s http://127.0.0.1:<live-port><health-path>'
```

The response body names the failing check. Fix the dependency; don't redeploy first, a new color will fail verification for the same reason.

## rollback: "artifact <sha> not on box"

Boxes keep the last 5 releases; older ones are pruned. `hadi releases -s <service>` shows what's available. For anything older, check out that commit and deploy it:

```bash
git checkout <sha> && hadi deploy && git checkout -
```

## rollback: "sha <x> was deployed as ... before the switch to image artifacts"

Working as intended: rollback won't cross an artifact-kind switch, because the current deploy.json can't restore an artifact of a different kind. Do what the message says — restore that era's deploy.json (`git log deploy.json`) and run `hadi deploy`.

## image: "not found in docker or podman" / "exists in BOTH ... with different IDs"

The `image:` artifact must exist as a local tag where hadi runs. Not found: run your `build` (or drop `--skip-build`). Found in both engines with different IDs: you built with one engine and have a stale tag in the other — remove the stale one (`docker rmi <tag>` or `podman rmi <tag>`) so hadi can't ship the wrong build.

## image: env "has a quoted value"

Working as intended for image services: podman reads `/etc/<name>/env` literally, so `FOO="a b"` would put actual quotes in the value (systemd used to strip them). Unquote the value — unquoted spaces are fine in both worlds.

## image: once_before_flip fails with "/bin/sh: not found"

For image artifacts the hook runs inside a one-shot container of the new sha via `/bin/sh -c`, so the image must contain a shell. Distroless images can't use `once_before_flip`; run migrations another way or add a shell to the image.

## CI: `go install github.com/mitteai/hadi@vX.Y.Z` fails

The tag doesn't exist, or the runner can't reach GitHub. `git ls-remote --tags https://github.com/mitteai/hadi` shows real tags. If the module proxy is lagging a fresh tag, `GOPROXY=direct` in that step.

## No certificate on a domain entry

Caddy retries issuance on its own; the journal says what Let's Encrypt objected to:

```bash
hadi exec -s <service> 'journalctl -u caddy -n 30 --no-pager | grep -iE "acme|cert|challenge"'
```

Usual causes: the public A record doesn't point at this box yet, port 80/443 blocked, or a CDN proxying the domain so challenges never arrive (see ssl.md).

## healthz reports the wrong version

Your `build` command isn't stamping it. The version your binary reports comes from your build flags, and deploy.json's `build` is the single build definition; stamp it there:

```json
"build": "go build -ldflags \"-X main.version=$(git rev-parse --short HEAD)\" -o bin/api ."
```

## When you're truly stuck

The box always has the whole story:

```bash
hadi ssh -s <service>
cat /opt/<service>/hadi.json          # what hadi last did
cat /etc/caddy/hadi/<service>.caddy   # where traffic actually goes
systemctl status <service>@<port>     # both colors, one at a time
```

Nothing hadi does is hidden: it's systemd units, one proxy file, and one state file, all readable.
