# hadi

Zero-downtime deploys for plain Linux services on your own servers. No containers required.

hadi ships your service straight to systemd boxes: the new version starts next to the old one, gets health-checked, and only then receives traffic. If anything goes wrong, the old version never stops serving. There is no registry, no agent, and no platform to run: one static binary on your side, SSH on the other.

Each service describes itself in one small `deploy.json`. hadi handles the rest: deploys, rollbacks, config changes, logs, and automatic HTTPS.

## What hadi is, and what it isn't

hadi **is**:

- A deploy tool: one static binary that ships plain Linux services with zero downtime.
- Health-gated: a new version gets traffic only after proving itself; a failed deploy leaves the old version serving.
- Agentless: it runs on your machine or in CI and speaks SSH. Your servers run your service, systemd, and Caddy. Nothing else.
- The owner of the deploy surface: it generates the systemd unit and proxy config from `deploy.json`, so they can't drift from the repo.

hadi is **not**:

- A container platform. No images, no registry, no Docker required (a service may still use containers as sidecars).
- A provisioner. terraform or your own tooling creates boxes, users, system packages, and DNS. hadi begins where they end.
- A platform. No daemon, no agent, no web UI, no database. When hadi exits, systemd and Caddy are in charge.
- An orchestrator. No autoscaling, no canaries, no traffic splitting. Two versions, one live, deploys alternate.
- A secrets manager. The env lives on your boxes; hadi edits and ships it, and stores nothing anywhere else.

## Install

```bash
go install github.com/mitteai/hadi@latest
```

The binary lands in `$(go env GOPATH)/bin` (usually `~/go/bin`; make sure it's on your `PATH`). hadi runs on your machine and in CI, never on the servers it deploys to.

In CI, pin a version so upgrades are deliberate:

```yaml
- run: go install github.com/mitteai/hadi@v0.1.0
- run: $(go env GOPATH)/bin/hadi deploy
  env:
    HADI_SSH_KEY: ${{ secrets.DEPLOY_SSH_KEY }}
```

## Quick start

Add a `deploy.json` to your service repo:

```json
{
  "name": "forms",
  "zone": "example.com",
  "build": "make build-linux",
  "artifact": "bin/forms-linux",
  "run": {"port_env": "PORT"},
  "entry": {"domain": "forms.example.com"}
}
```

Then:

```bash
hadi check     # validate the config, print the plan
hadi deploy    # build, ship, verify, switch traffic
```

That's a live HTTPS service. Certificates are issued and renewed automatically. Full walkthrough with a hello-world server: [docs/quick-start.md](docs/quick-start.md).

## What you need

- Linux boxes running systemd (Debian-family), with root SSH access
- The service's user created by your provisioning
- A DNS record per service for discovery: `<name>.boxes.<zone>` pointing at its boxes (or set `"hosts"` in deploy.json)

The only credential hadi uses is an SSH key: `HADI_SSH_KEY` or `--ssh-key`. Details and a preflight checklist: [docs/requirements.md](docs/requirements.md).

## Commands

```
deploy   [--skip-build]         build, ship, verify, switch traffic, drain
check                           validate deploy.json, print the plan
env      edit|set|unset|push|pull    edit a service's env remotely; changes apply with zero downtime
releases                        deploy history: sha, when, who
rollback [--to <sha>]           restore an earlier release, safely
status                          what's live, since when, healthy or not
ls       --zone <zone>          every service at a glance
logs     [-f] [-n N]            follow the service's logs across boxes
ssh      [box]                  shell into a box
exec     '<cmd>'                run a command on every box
ensure                          prepare a box (idempotent; usable from Packer)
update                          update hadi itself to the latest release
```

Inside a service repo, commands read `./deploy.json`, including the zone for fleet commands like `ls`. From anywhere else, use `-s <service>` with `--zone <zone>` (or set `HADI_ZONE`).

Examples:

```bash
hadi deploy                                # from the service repo: build and ship
hadi deploy --host 10.0.0.5                # one box only
hadi env set -s api STRIPE_KEY=sk_live_x   # rotate one secret, zero downtime
hadi env pull -s api > api.env             # snapshot before risky work
hadi env push -s api api.env               # restore it
hadi rollback -s api                       # back to the previous release
hadi rollback -s api --to 3f2c91a          # back to a specific one
hadi logs -s api -f                        # follow logs across all boxes
hadi exec -s api 'systemctl status caddy'  # run something everywhere
hadi ls --zone example.com                 # the whole fleet, one table
hadi update                                # get the newest hadi
```

## deploy.json reference

Only `name`, `zone`, `entry`, and `run.port_env` are required. Everything else has a default.

| Key | What it is | Default |
|---|---|---|
| `name` | Service name. Owns `/opt/<name>`, `/etc/<name>/env`, and the unit names. | required |
| `zone` | DNS zone the discovery records live under. | required |
| `entry` | Where traffic enters: `{"port": N}` (internal, behind your LB) or `{"domain": "x.example.com"}` (public, automatic HTTPS). Exactly one. | required |
| `hosts` | Explicit box list (DNS names or IPs). | resolve `<name>.boxes.<zone>` |
| `build` | Shell command that produces the artifact. | none |
| `artifact` | Path to the built binary, or a `.tgz` release unpacked per deploy. | required for deploy |
| `colors` | The two internal ports the service alternates between. | port entry: front+1, front+2; domain entry: 4001, 4002 |
| `health` | HTTP path polled to verify a new version. | `/healthz` |
| `files` | Extra files to ship: `{"local/path": "/remote/path"}`. | none |
| `extra_units` | Directory of additional systemd units to ship (timers, helpers). | none |
| `run.port_env` | Env var your service reads its listen port from. Must not appear in the env file. | required |
| `run.user` | Unix user the service runs as. | `name` |
| `run.exec` | Command the unit starts. | `/opt/<name>/bin/<name>` |
| `run.after`, `run.requires` | Extra systemd ordering and dependencies. | none |
| `run.stop_timeout_sec` | How long a draining old version may keep running. | 90 |
| `run.ready_timeout_sec` | How long to wait for a new version to become healthy. | 60 |
| `run.ambient_caps` | Kernel capabilities (e.g. `CAP_NET_ADMIN`). | none |
| `run.read_write_paths` | Writable paths under the hardened unit. | none |
| `run.env_extra` | Fixed env vars baked into the unit. | none |
| `run.delegate` | cgroup controllers to delegate, e.g. `["cpu", "io", "memory", "pids"]`. | none |
| `run.unit_file` | Hand-written unit template; disables unit generation. | generated |
| `entry.body_max` | Request body size limit at the proxy. | proxy default |
| `entry.proxy_timeout` | Read/write timeout at the proxy, for long requests. | proxy default |
| `hooks.before_start` | Runs on each box before the new version starts (e.g. refresh a sidecar). | none |
| `hooks.once_before_flip` | Runs once per deploy, after verification, before traffic moves (e.g. migrations). | none |
| `hooks.after_flip` | Runs on each box after traffic has moved. | none |

Hooks must be idempotent: rerunning a failed deploy reruns them.

## Docs

- [Quick start](docs/quick-start.md): hello world to production, end to end
- [Requirements](docs/requirements.md): what boxes need, with a preflight checklist
- [Commands](docs/commands.md): every command, its flags, and examples
- [deploy.json](docs/config.md): every option, with defaults and examples
- [CI](docs/ci.md): the complete workflow, one secret, version pinning
- [DNS and inventory](docs/dns.md): the two record families and why DNS is the registry
- [SSL](docs/ssl.md): automatic HTTPS, how renewal works, what to check
- [Terraform](docs/terraform.md): the boundary, a complete example, what cloud-init should not do
- [How it works](docs/how-it-works.md): the lifecycle, colors, discovery, and where truth lives
- [Troubleshooting](docs/troubleshooting.md): failure scenarios and how to debug them fast
