# hadi

Zero-downtime deploys to your own servers. Ship your Docker image — or a binary, or a release tarball — same commands, same blue-green flip. Describe each service in a small `deploy.json`; hadi handles the rest: deploys, rollbacks, config changes, logs, and automatic HTTPS.

Nothing runs on your boxes but systemd and Caddy — no agents, no daemons, no registry, no platform to operate. The only credential is an SSH key. Why this shape and not Kamal, Dokku, or Kubernetes: [docs/why-hadi.md](docs/why-hadi.md).

## Install

```bash
$ go install github.com/mitteai/hadi@latest
```

For CI setup, see [docs/ci.md](docs/ci.md).

## Quick start

Your repo has a Dockerfile. Add a `deploy.json` next to it:

```json
{
  "name": "forms",
  "zone": "example.com",
  "entry": {"domain": "forms.example.com"}
}
```

Then:

```bash
hadi check     # validate the config, print the plan
hadi deploy    # build the Dockerfile, ship, verify, switch traffic
```

That's a live HTTPS service. Certificates are issued and renewed automatically. The Dockerfile is the build declaration — hadi builds it locally and streams the image over SSH; no registry involved, ever, and no Docker daemon on your boxes (containers run under daemonless podman, supervised by systemd). Full walkthrough: [docs/quick-start.md](docs/quick-start.md).

No Dockerfile? Ship a binary or a release tarball instead — same commands, same flip. State how to build it and what to ship, and nothing else changes:

```json
"build": "make build-linux",
"artifact": "bin/forms-linux"
```

Walkthrough: [docs/no-docker.md](docs/no-docker.md).

**Example commands**:

* `hadi ls`: list all services. 
* `hadi boxes`: list all boxes.
* `hadi top`: live dashboard of services, boxes, vitals and streaming logs.
* `hadi logs -f`: watch logs of all services.
* `hadi env edit`: edit env variables.
* `hadi env -s myapp MY_ENV_VAR=123`: set environment variable.
* `hadi rollback`: restore to an earlier release.
* `hadi rm -s myapp`: retire a service from its boxes (units, site, artifacts, env).
* `hadi ssh -s myapp`: ssh into the box running `myapp` service.
* `hadi exec -s myapp '<command>'`: run command in remote box(es).

Read more about Hadi commands: [Commands](docs/commands.md).

## Docs

- [Why hadi](docs/why-hadi.md): the bets, honestly compared with Kamal, Dokku, and Kubernetes
- [Quick start](docs/quick-start.md): Dockerfile to production, end to end
- [Image deploys, in depth](docs/docker.md): what runs on the box — no registry, no daemon
- [Deploying without Docker](docs/no-docker.md): ship a plain binary or release tarball — same commands
- [Requirements](docs/requirements.md): what boxes need, with a preflight checklist
- [Commands](docs/commands.md): every command, its flags, and examples
- [deploy.json](docs/config.md): every option, with defaults and examples
- [CI](docs/ci.md): the complete workflow, one secret, version pinning
- [DNS and inventory](docs/dns.md): the two record families and why DNS is the registry
- [SSL](docs/ssl.md): automatic HTTPS, how renewal works, what to check
- [Terraform](docs/terraform.md): the boundary, a complete example, what cloud-init should not do
- [How it works](docs/how-it-works.md): the lifecycle, colors, discovery, and where truth lives
- [Troubleshooting](docs/troubleshooting.md): failure scenarios and how to debug them fast
