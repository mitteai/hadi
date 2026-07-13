# Commands

Conventions that apply everywhere:

- Inside a service repo, commands read `./deploy.json`. From anywhere else, `-s <service>` resolves the service through DNS and reads its config from the box itself.
- The zone comes from `--zone`, then the `zone` of a `deploy.json` in the current directory, then `HADI_ZONE`.
- `--host <address>` restricts any command to one box, bypassing discovery.
- The only credential is an SSH key: `HADI_SSH_KEY` (contents or path), `--ssh-key <path>`, or `~/.ssh/id_ed25519`.
- Exit codes: `0` success, `1` failed but the service is safe on the previous version, `2` config or usage error, nothing touched.

## deploy

```
hadi deploy [--skip-build] [--host <addr>]
```

The full lifecycle: run `build` from deploy.json (`--skip-build` uses the existing artifact), ship the artifact plus configured `files` and units, run `before_start`, start the new version on the idle port, poll its health endpoint until `ready_timeout_sec`, run `once_before_flip` (first box only), flip the proxy, confirm through the front door, drain the old version without blocking. Multi-box fleets deploy sequentially and stop at the first failure.

If verification fails, hadi prints the new version's last journal lines and health response, stops it, and exits 1 with the old version still serving.

```bash
hadi deploy                          # from the service repo
hadi deploy --skip-build             # artifact already built
hadi deploy --host 10.0.0.5          # one box, no DNS
```

## check

```
hadi check
```

Validate `deploy.json` and print the plan: entry, colors, timeouts, resolved boxes, and the generated systemd unit. Touches nothing. Run it in CI on pull requests.

## env

The box is the source of truth for a service's environment; hadi edits it remotely. Every change applies with a zero-downtime flip: the new version boots under the new env and must pass health checks before traffic moves, so a broken value can't take the service down.

```
hadi env edit  [-s <service>]              # $EDITOR; save ships + flips, abort does nothing
hadi env set   [-s <service>] KEY=VALUE... # patch values in place, flip
hadi env unset [-s <service>] KEY...       # remove values, flip
hadi env push  [-s <service>] <file>       # full replace from a file, flip. Never a merge.
hadi env pull  [-s <service>] [file]       # fetch to stdout or a file; warns if boxes differ
```

```bash
hadi env set -s api STRIPE_KEY=sk_live_xxx   # rotate one secret
hadi env pull -s api > api.env               # snapshot before risky work
hadi env push -s api api.env                 # restore it
hadi env pull -s api | grep R2_              # quick audit
```

hadi refuses to ship an env that sets the service's port variable (`run.port_env`): the unit injects the per-color port, and an env value would override it and break the flip.

## releases

```
hadi releases [-s <service>]
```

The deploy ledger per box: timestamp, sha, color, deployer. Each box keeps its last 5 artifacts, so ledger depth equals rollback depth.

## rollback

```
hadi rollback [-s <service>] [--to <sha>]
```

Restore an earlier artifact (default: the previous one), start it on the idle port, verify, flip. Identical safety to a deploy: a rollback that doesn't verify leaves the current version serving.

```bash
hadi rollback -s api                 # previous release
hadi rollback -s api --to 3f2c91a    # a specific one (see hadi releases)
```

## status

```
hadi status [-s <service>] [--host <addr>]
```

Per box: live color (read from the proxy, the source of truth), deployed sha, when, by whom, health right now, and the rollback target.

## ls

```
hadi ls [--zone <zone>]
```

Every hadi-managed service: box count, live color, sha, health, entry. Resolved from the `_hadi.<zone>` TXT record plus each box's own state file. No repo checkout needed.

## logs

```
hadi logs [-s <service>] [-f] [-n <lines>] [--unit <name>]
```

journalctl for the live color across all boxes, host-prefixed. `-f` follows, interleaving lines as they arrive (no timestamp merging). `--unit` targets an auxiliary unit shipped via `extra_units` (a timer, a helper) instead of the service itself.

```bash
hadi logs -s api -f
hadi logs -s api -n 500
hadi logs -s api --unit api-cleanup.timer
```

## ssh, exec

```
hadi ssh  [-s <service>] [box]
hadi exec [-s <service>] '<command>'
```

`ssh` opens an interactive shell (with one box, no argument needed; with several, it lists them). `exec` runs a command on every box as root and prints output per host; non-zero anywhere exits 1.

```bash
hadi ssh -s api
hadi exec -s api 'systemctl status caddy'
hadi exec -s api 'df -h /var'
```

## ensure

```
hadi ensure [--config <path>] [--host <addr>]
```

Converge hadi's layer on the boxes: Caddy installed and enabled, the service's proxy site, directories, env file permissions. Idempotent, and runs implicitly on every deploy. Use it as a Packer provisioner step to pre-bake golden images; boxes without it simply converge on their first deploy.

## update

```
hadi update
```

Replace this binary with the latest GitHub release, verified against the release's `sha256sums.txt`, swapped atomically over the running executable. For workstations. CI pins a version on purpose, and no other command ever checks for updates.

## version

```
hadi version
```
