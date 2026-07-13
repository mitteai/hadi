# Using hadi with terraform

The boundary, stated once: **terraform owns machines, hadi owns deployment.** terraform creates boxes, users, system packages, DNS records, and load balancers. hadi moves software onto boxes that already exist, and converges only its own layer (Caddy, units, directories) at deploy time. Neither tool calls the other; the DNS records are the handshake.

hadi doesn't require terraform. Anything that can create a Linux box, a user, and two DNS records works, including your hands. terraform is just the tool that makes the no-drift guarantee automatic, because the box and its discovery record live in the same resource graph.

## A complete example

One service, two boxes, discovery records, on Hetzner + Cloudflare (any provider pair works the same way):

```hcl
variable "api_count" { default = 2 }

resource "hcloud_server" "api" {
  count       = var.api_count
  name        = "api-${count.index}"
  server_type = "cpx22"
  image       = "ubuntu-24.04"
  ssh_keys    = [hcloud_ssh_key.deploy.id]

  user_data = <<-CLOUDINIT
    #!/bin/bash
    set -e
    # terraform's half of the contract: the user and system deps.
    # hadi's ensure handles Caddy, dirs, and units on first deploy.
    useradd --system --create-home --home /opt/api --shell /usr/sbin/nologin api
    apt-get update -y && apt-get install -y curl
  CLOUDINIT
}

# The discovery record, in the same graph as the box: they can't drift.
resource "cloudflare_record" "api_boxes" {
  count   = var.api_count
  zone_id = var.cloudflare_zone_id
  name    = "api.boxes"
  content = hcloud_server.api[count.index].ipv4_address
  type    = "A"
  ttl     = 60
  proxied = false   # must resolve to the box itself
}

# The fleet list, feeding `hadi ls`.
resource "cloudflare_record" "hadi_services" {
  zone_id = var.cloudflare_zone_id
  name    = "_hadi"
  content = "api"          # comma-separated as services grow: "api,worker,web"
  type    = "TXT"
  ttl     = 60
}
```

With that applied, the service repo needs only its `deploy.json` and `hadi deploy` works. Scale by changing `api_count`: the next deploy covers the new boxes with zero config edits anywhere else.

## What cloud-init should and shouldn't do

Should: create the service's user, install system dependencies the service needs at runtime (ffmpeg, whatever), set up monitoring agents, firewalls, kernel settings. Machine things.

Shouldn't: install Caddy, write proxy config, create systemd units for the service, or place binaries. That's hadi's layer; `ensure` converges it idempotently on every deploy, and duplicating it in cloud-init recreates the drift problem hadi exists to end. A freshly provisioned box intentionally serves nothing until its first deploy.

## Load balancers (port entries)

For `"entry": {"port": N}` services, terraform also owns the LB. Point its health check at the service's health path on the front port; hadi's flip keeps that green through deploys:

```hcl
resource "hcloud_load_balancer_service" "api" {
  load_balancer_id = hcloud_load_balancer.api.id
  protocol         = "https"
  listen_port      = 443
  destination_port = 4002        # matches entry.port

  health_check {
    protocol = "http"
    port     = 4002
    interval = 10
    http { path = "/healthz" }   # matches deploy.json health
  }
}
```

Domain entries need none of this: hadi's Caddy terminates TLS on the box, and terraform's only job is the public A record pointing at it.

## Packer, optionally

`hadi ensure --config deploy.json` works as a provisioner step, pre-baking Caddy and the directory layout into a golden image so first deploys on fresh boxes skip even that. It's never required: ensure is idempotent, so plain cloud-init boxes converge on their first deploy and golden-image boxes just converge faster.

## The SSH key

terraform installs the public half on the boxes (`hcloud_ssh_key` above); hadi holds the private half (`HADI_SSH_KEY` locally, one secret in CI). That key is the entire trust relationship between hadi and the fleet: rotating it is a terraform apply plus a secret update, and revoking it severs deploys completely.
