# hadi

Zero-downtime deploys for plain Linux services on your own servers. No containers required.

hadi ships your service straight to systemd boxes: the new version starts next to the old one, gets health-checked, and only then receives traffic. If anything goes wrong, the old version never stops serving. There is no registry, no agent, and no platform to run: one static binary on your side, SSH on the other.

Each service describes itself in one small `deploy.json`. hadi handles the rest: deploys, rollbacks, config changes, logs, and automatic HTTPS.

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

That's a live HTTPS service. Certificates are issued and renewed automatically.

## What you need

- Linux boxes running systemd (Debian-family), with root SSH access
- The service's user created by your provisioning
- A DNS record per service for discovery: `<name>.boxes.<zone>` pointing at its boxes (or set `"hosts"` in deploy.json)

The only credential hadi uses is an SSH key: `HADI_SSH_KEY` or `--ssh-key`.

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
```

Inside a service repo, commands read `./deploy.json`. From anywhere else, use `-s <service>` with `--zone <zone>` (or set `HADI_ZONE`).

## How a deploy works

```
build → ship → start new version on the idle port → health-check it
→ switch the proxy → confirm through the front door → drain the old one
```

If the new version never becomes healthy, nothing is switched and the old version keeps serving. Rollback is the same flow with an earlier release.

## Entry modes

- `"entry": {"port": 4002}`: internal service behind your load balancer; TLS terminates there.
- `"entry": {"domain": "api.example.com"}`: public service; hadi's on-box Caddy terminates TLS with automatic certificates.
