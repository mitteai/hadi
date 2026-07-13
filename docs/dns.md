# DNS and inventory

hadi has no inventory file, no server list in config, and no registry service. DNS is the inventory, and your provisioning tool is its only writer.

## The records

Two record families per zone:

```
<name>.boxes.<zone>     one A record per box of that service       TTL 60
_hadi.<zone>            one TXT record listing the service names    TTL 60
```

Example, for a fleet of three services on `example.com`:

```
api.boxes.example.com.      60  IN  A    10.0.0.5
api.boxes.example.com.      60  IN  A    10.0.0.6
worker.boxes.example.com.   60  IN  A    10.0.0.7
web.boxes.example.com.      60  IN  A    10.0.0.8
_hadi.example.com.          60  IN  TXT  "api,worker,web"
```

Keep the `boxes` records DNS-only (grey-cloud on Cloudflare): they must resolve to the box IPs themselves, and they exist for hadi, not for users.

## How commands use them

- `hadi deploy`, `env`, `status`, `logs`, `ssh`, `exec` with `-s <name>`: resolve `<name>.boxes.<zone>`, connect to every IP.
- `hadi ls`: read `_hadi.<zone>` for the service list, resolve each service's boxes, then read each box's own `/opt/<name>/hadi.json` for what's running. The table you see is assembled from DNS plus box state and nothing else.
- Inside a service repo, discovery uses the repo's `deploy.json` (`name` + `zone`); `-s` is for everywhere else.

Overrides, in increasing strength: `"hosts": [...]` in deploy.json replaces DNS discovery with a static list; `--host <addr>` restricts any single command to one box.

## Why DNS

An inventory has to be highly available, globally readable, cached sensibly, and impossible to drift from reality. That's a description of DNS, which has existed for four decades:

- **No token to read it.** Anyone with the SSH key can operate the fleet; there's no cloud API credential for read paths, and a cloud API outage can't lock you out of `hadi ssh`.
- **No staleness policy to invent.** TTL 60 is the policy, enforced by resolver infrastructure you don't run. hadi keeps no cache of its own.
- **No drift, by construction.** Declare the record and the box in the same provisioning graph (see the terraform doc) and they change together or not at all. Replace a box; the same apply moves the record; the next deploy finds the new box.
- **Scaling is invisible to config.** Going from 1 box to 3 adds A records. Every `deploy.json`, workflow, and habit stays identical; the next deploy simply covers three boxes.

This is the same shape Consul's DNS interface and Kubernetes headless services converged on (resolve a service name, get member IPs), with zero agents, because your DNS host already runs the infrastructure.

## Per-box state completes the picture

DNS answers "what exists and where". Each box answers "what's running on me" via `/opt/<name>/hadi.json`: live color, deployed sha, previous sha, deployer, timestamp, and a snapshot of the service's config. Together they form a complete inventory that nobody maintains and that can't rot: the records are declared by provisioning, the state is written by deploys.

One honest caveat: `boxes` records publish origin IPs in public DNS. Anyone can enumerate your boxes and knock. Firewalls on the boxes are therefore load-bearing, and your services should carry their own auth regardless.
