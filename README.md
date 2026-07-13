# hadi

Zero-downtime deploys for plain Linux services on your own servers. No containers required. Describe your services in small `deploy.json` files, hadi handles the rest: deploys, rollbacks, config changes, logs, and automatic HTTPS.

## Install

```bash
$ go install github.com/mitteai/hadi@latest
```

For CI setup, see [docs/ci.md](docs/ci.md).

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

**Example commands**:

* `hadi ls`: list all services. 
* `hadi boxes`: list all boxes.
* `hadi top`: live dashboard of services, boxes, vitals and streaming logs.
* `hadi logs -f`: watch logs of all services.
* `hadi env edit`: edit env variables.
* `hadi env -s myapp MY_ENV_VAR=123`: set environment variable.
* `hadi rollback`: restore to an earlier release.
* `hadi ssh -s myapp`: ssh into the box running `myapp` service.
* `hadi exec -s myapp '<command>'`: run command in remote box(es).

Read more about Hadi commands: [Commands](docs/commands.md).

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
