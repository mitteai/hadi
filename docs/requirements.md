# What you need

The complete list:

- Linux boxes running systemd (Debian-family), with root SSH access
- The service's user, created by your provisioning
- A DNS record per service for discovery: `<name>.boxes.<zone>` pointing at its boxes (or `"hosts"` in deploy.json)
- One credential: an SSH key, via `HADI_SSH_KEY` or `--ssh-key`

Everything else on the box (Caddy, directories, the systemd units, the proxy config) hadi installs and converges itself. Here's each requirement in detail, with how to check it.

## The boxes

Any Linux with systemd works as the process supervisor; **Debian-family** (Debian, Ubuntu) is assumed because `hadi ensure` installs Caddy through apt. On another family, pre-install Caddy yourself and ensure skips that step.

Nothing else is expected: no Docker, no Python, no agents. hadi's remote operations use only tools present on every minimal install (`systemctl`, `curl`, `tar`, `journalctl`). The one exception is opt-in: services with an `image:` artifact need podman and zstd on the box, and `hadi ensure` installs both through apt — still no daemon, no registry, no agent.

For image artifacts the *local* side (workstation or CI runner) additionally needs the engine that built the tag (docker or podman) and `zstd`, since the image is saved and compressed where it was built.

```bash
ssh root@box 'systemctl --version | head -1 && command -v curl apt-get'
```

## Root SSH

hadi connects as root: it writes to `/etc`, manages systemd units, and reloads Caddy. The transport is hadi's entire relationship with your boxes; there is nothing to install or open besides sshd, which is already there.

Host keys are not verified. If that matters for your threat model, distribute known_hosts through provisioning and front hadi with your own SSH config.

## The service user

Each service runs as an unprivileged user (`run.user`, defaulting to the service name). Creating users is a machine concern, so it belongs to your provisioning, not to hadi; `hadi ensure` fails with a clear message if the user is missing.

```bash
useradd --system --create-home --home /opt/<name> --shell /usr/sbin/nologin <name>
```

## The DNS records

One A record set per service, `<name>.boxes.<zone>`, one record per box, low TTL, DNS-only. Optionally `_hadi.<zone>` (TXT listing service names) to power `hadi ls`. Details and rationale in [dns.md](dns.md); publishing them from terraform in [terraform.md](terraform.md).

No DNS? `"hosts": ["10.0.0.5"]` in deploy.json replaces discovery entirely.

## The SSH key

The one credential. hadi looks for it as `--ssh-key <path>`, then `HADI_SSH_KEY` (PEM contents or a path), then `~/.ssh/id_ed25519`. In CI it's a single secret; on a workstation it's usually just your existing key. Whoever holds it can deploy; nobody else can.

## Ports

- **22** reachable from wherever hadi runs (workstation, CI).
- **80 and 443** reachable publicly for domain entries; Let's Encrypt validates over them ([ssl.md](ssl.md)).
- **The front port** reachable by your load balancer for port entries.
- The color ports bind localhost-facing and need no exposure; keeping them firewalled is good hygiene.

## Preflight

From your machine, with the key in place:

```bash
ssh root@<box> 'id <name> && systemctl --version >/dev/null && echo box-ok'
dig +short <name>.boxes.<zone>        # your box IPs, or use "hosts"
hadi check                            # validates deploy.json, prints the plan
```

If all three answer cleanly, `hadi deploy` will work.
