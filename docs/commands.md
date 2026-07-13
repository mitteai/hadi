# Commands

## Conventions

- Inside a service repo, commands read `./deploy.json`. From anywhere else, `-s <service>` resolves the service through DNS and reads its config from the box itself.
- Exit codes: `0` success · `1` failed, but the service is safe on the previous version · `2` config or usage error, nothing touched.

Global flags, accepted by every command that touches a service:

| Flag | What it does | Example |
|---|---|---|
| `-s <service>` | Target a service by name from outside its repo. | `hadi status -s api` |
| `--zone <zone>` | DNS zone for discovery. Precedence: this flag, then the `zone` in a local `deploy.json`, then `HADI_ZONE`. | `hadi ls --zone example.com` |
| `--host <addr>` | Restrict the command to one box, bypassing discovery. | `hadi deploy --host 10.0.0.5` |
| `--ssh-key <path>` | SSH private key. Default: `HADI_SSH_KEY` (contents or path), then `~/.ssh/id_ed25519`. | `hadi deploy --ssh-key ~/.ssh/deploy` |

---

## deploy

```
hadi deploy [--skip-build] [--host <addr>]
```

The full lifecycle: run `build` from deploy.json, ship the artifact plus configured `files` and units, run `before_start`, start the new version on the idle port, poll its health endpoint, run `once_before_flip` (first box only), flip the proxy, confirm through the front door, drain the old version. Multi-box fleets deploy sequentially and stop at the first failure.

If verification fails, hadi prints the new version's last journal lines and health response, stops it, and exits 1 with the old version still serving.

```bash
hadi deploy                    # from the service repo
hadi deploy --skip-build       # artifact is already built
```

| Flag | What it does | Example |
|---|---|---|
| `--skip-build` | Skip the `build` command; ship the existing artifact. | `hadi deploy --skip-build` |
| `--host <addr>` | Deploy to one box only (testing, a fresh box). | `hadi deploy --host 10.0.0.5` |
| `--ssh-key <path>` | Key override for this run. | `hadi deploy --ssh-key ./ci_key` |

## check

```
hadi check
```

Validate `deploy.json` and print the plan: entry, colors, timeouts, resolved boxes, the generated systemd unit. Touches nothing; run it in CI on pull requests.

No flags. Reads `./deploy.json` only.

## env

The box is the source of truth for a service's environment. Every change applies with a zero-downtime flip: the new version boots under the new env and must pass health checks before traffic moves, so a broken value can't take the service down.

hadi refuses to ship an env that sets `run.port_env`: the unit injects the per-color port, and an env value would override it and break the flip.

```bash
hadi env edit -s api                        # $EDITOR; save ships + flips, abort does nothing
hadi env set -s api STRIPE_KEY=sk_live_xxx  # rotate one secret
hadi env unset -s api OLD_FLAG              # remove a value
hadi env pull -s api > api.env              # snapshot before risky work
hadi env push -s api api.env                # full replace from a file. Never a merge.
```

| Flag / argument | What it does | Example |
|---|---|---|
| `edit` | Pull into `$EDITOR`, ship + flip on save. | `hadi env edit -s api` |
| `set KEY=VALUE...` | Patch values in place, then flip. Values may contain `=`. | `hadi env set -s api TOKEN=a=b==` |
| `unset KEY...` | Remove values, then flip. | `hadi env unset -s api OLD_FLAG` |
| `push <file>` | Replace the entire env from a file, then flip. | `hadi env push -s api api.env` |
| `pull [file]` | Fetch to stdout or a file; warns if boxes differ. | `hadi env pull -s api > api.env` |
| `-s <service>` | Which service's env. | `hadi env pull -s worker` |
| `--host <addr>` | Operate on one box's env only. | `hadi env pull -s api --host 10.0.0.5` |

## releases

```
hadi releases [-s <service>]
```

The deploy ledger per box: timestamp, sha, color, deployer. Each box keeps its last 5 artifacts, so ledger depth equals rollback depth.

| Flag | What it does | Example |
|---|---|---|
| `-s <service>` | Which service's ledger. | `hadi releases -s api` |
| `--host <addr>` | One box's ledger only. | `hadi releases -s api --host 10.0.0.5` |

## rollback

```
hadi rollback [-s <service>] [--to <sha>]
```

Restore an earlier artifact, start it on the idle port, verify, flip. Identical safety to a deploy: a rollback that doesn't verify leaves the current version serving.

```bash
hadi rollback -s api                 # previous release
hadi rollback -s api --to 3f2c91a    # a specific one (see hadi releases)
```

| Flag | What it does | Example |
|---|---|---|
| `--to <sha>` | Target release. Default: the previous one. | `hadi rollback -s api --to 3f2c91a` |
| `-s <service>` | Which service. | `hadi rollback -s api` |
| `--host <addr>` | Roll back one box only. | `hadi rollback -s api --host 10.0.0.5` |

## status

```
hadi status [-s <service>]
```

Per box: live color (read from the proxy, the source of truth), deployed sha, when, by whom, health right now, the rollback target, and box vitals (load, memory, disk). Vitals are kernel counters read over the same SSH session; the cost is the round-trip, never the service.

| Flag | What it does | Example |
|---|---|---|
| `-s <service>` | Which service. | `hadi status -s api` |
| `--host <addr>` | One box only. | `hadi status --host 10.0.0.5` |

## ls

```
hadi ls [--zone <zone>]
```

Every hadi-managed service: box count, live color, sha, health, entry. Resolved from the `_hadi.<zone>` TXT record plus each box's own state file. No repo checkout needed.

| Flag | What it does | Example |
|---|---|---|
| `--zone <zone>` | Zone to list. Falls back to a local `deploy.json`, then `HADI_ZONE`. | `hadi ls --zone example.com` |
| `--ssh-key <path>` | Key used to read box state. | `hadi ls --zone example.com --ssh-key ./key` |

## boxes

```
hadi boxes [-s <service>] [--zone <zone>] [-q]
```

Where a service lives, in the same table style as `hadi ls` but one row per box: service, address, live color, sha, health. Bare in a service repo: that service's boxes. With an explicit `--zone`: the whole fleet, even from inside a repo.

`-q` prints plain addresses, one per line, with no SSH at all: instant, key-free, made for feeding other commands.

```bash
hadi boxes --zone example.com             # the fleet, one row per box
hadi ssh $(hadi boxes -q | head -1)       # shell into the first box
for b in $(hadi boxes -q -s api); do ...  # iterate a service's boxes
```

| Flag | What it does | Example |
|---|---|---|
| `-q` | Plain addresses only, one per line. No SSH, no key needed. | `hadi boxes -q -s api` |
| `-s <service>` | One service's boxes. | `hadi boxes -s api` |
| `--zone <zone>` | Explicit zone lists the whole fleet, even from inside a repo. | `hadi boxes --zone example.com` |
| `--ssh-key <path>` | Key for the health column (table mode only). | `hadi boxes --ssh-key ./key` |

## logs

```
hadi logs [-s <service>] [-f] [-n <lines>] [--unit <name>]
```

journalctl for the live color across all boxes, host-prefixed. `-f` follows, interleaving lines as they arrive (no timestamp merging).

```bash
hadi logs -s api -f
hadi logs -s api --unit api-cleanup.timer
```

| Flag | What it does | Example |
|---|---|---|
| `-f` | Follow across all boxes. | `hadi logs -s api -f` |
| `-n <lines>` | How many lines back. Default 100. | `hadi logs -s api -n 500` |
| `--unit <name>` | An auxiliary unit (timer, helper) instead of the live color. | `hadi logs -s api --unit api-cleanup.timer` |
| `-s <service>` | Which service. | `hadi logs -s worker -f` |
| `--host <addr>` | One box's logs only. | `hadi logs -s api --host 10.0.0.5 -f` |

## ssh

```
hadi ssh [-s <service>] [box]
```

Interactive shell on a box. With one box, no argument needed; with several, hadi lists them and asks you to pick.

| Flag / argument | What it does | Example |
|---|---|---|
| `[box]` | Which box, when the service has several. | `hadi ssh -s api 10.0.0.6` |
| `-s <service>` | Which service's boxes. | `hadi ssh -s api` |

## exec

```
hadi exec [-s <service>] '<command>'
```

Run a command on every box as root, output per host. Non-zero anywhere exits 1, so it works for fleet-wide assertions as well as poking around.

```bash
hadi exec -s api 'systemctl status caddy'
hadi exec -s api --host 10.0.0.5 'df -h /var'
```

| Flag / argument | What it does | Example |
|---|---|---|
| `'<command>'` | The command, quoted as one argument. | `hadi exec -s api 'uptime -p'` |
| `-s <service>` | Which service's boxes. | `hadi exec -s worker 'free -m'` |
| `--host <addr>` | One box only. | `hadi exec -s api --host 10.0.0.5 'ls /opt/api'` |

## ensure

```
hadi ensure [--config <path>] [--host <addr>]
```

Converge hadi's layer on the boxes: Caddy installed and enabled, the service's proxy site, directories, env file permissions. Idempotent; runs implicitly on every deploy. Use it as a Packer provisioner to pre-bake golden images.

| Flag | What it does | Example |
|---|---|---|
| `--config <path>` | Which deploy.json. Default `./deploy.json`. | `hadi ensure --config services/api/deploy.json` |
| `--host <addr>` | Converge one box. | `hadi ensure --host 10.0.0.5` |

## update

```
hadi update
```

Replace this binary with the latest GitHub release, verified against the release's `sha256sums.txt`, swapped atomically. For workstations; CI pins a version on purpose, and no other command ever checks for updates.

No flags.

## version

```
hadi version        # also: --version, -v
```
