# deploy.json

One file per service repo, at the root. Only `name`, `zone`, and `entry` are required; everything else has a default, so a config states only what deviates.

The smallest real config — for a repo with a Dockerfile, which is its own build declaration:

```json
{
  "name": "forms",
  "zone": "example.com",
  "entry": {"domain": "forms.example.com"}
}
```

The same service as a plain binary states how to build and what to ship:

```json
{
  "name": "forms",
  "zone": "example.com",
  "build": "make build-linux",
  "artifact": "bin/forms-linux",
  "entry": {"domain": "forms.example.com"}
}
```

Unknown keys are rejected, so typos fail loudly at `hadi check` instead of silently doing nothing.

## Top level

### name (required)

The service's identity on the boxes. It owns `/opt/<name>`, `/etc/<name>/env`, the systemd units (`<name>@.service`), and the proxy site. Keep it short and unix-friendly.

### zone (required)

The DNS zone discovery records live under. hadi resolves boxes via `<name>.boxes.<zone>`. This is the one key with no possible default.

### entry (required)

Where traffic enters. Exactly one of:

```json
"entry": {"port": 4002}                      // internal: your LB terminates TLS and targets this port
"entry": {"domain": "api.example.com"}       // public: on-box Caddy terminates TLS, certificates automatic
```

Optional proxy knobs on either form:

```json
"entry": {
  "port": 8080,
  "body_max": "250MB",        // request body limit at the proxy
  "proxy_timeout": "15m"      // read/write timeout, for long requests
}
```

### hosts

Explicit box list (DNS names or IPs) instead of DNS discovery:

```json
"hosts": ["api.example.com"]
```

Default: resolve `<name>.boxes.<zone>`.

### build

Shell command that produces the artifact, run locally (or on the CI runner) before shipping. Stamp your version here if your binary reports one:

```json
"build": "go build -ldflags \"-X main.version=$(git rev-parse --short HEAD)\" -o bin/api ."
```

With `artifact` set, omit `build` to always deploy a pre-built artifact.

**The Docker default:** when `build` and `artifact` are *both* absent and a `Dockerfile` sits next to deploy.json, hadi fills them in — `build` as `docker build --platform linux/amd64 -t <name>:hadi .` (podman if docker isn't installed locally) and `artifact` as `image:<name>:hadi`. `hadi check` prints both with a `(default: Dockerfile found)` label. Stating either key disables the inference entirely (check notes the unused Dockerfile), and hadi never reads the Dockerfile's contents. Both keys absent with no Dockerfile is a config error that names both roads.

### artifact

What gets shipped. Three kinds, detected by prefix or extension:

- A **container image** (`image:<local tag>`, the default via the Dockerfile inference above): your `build` leaves the tag in the local docker or podman; hadi saves it through zstd, streams it over SSH (no registry, ever), loads it on the box, and tags `localhost/<name>:<sha>` plus a moving `:current` tag — the image analogue of the `current` symlink. Write it explicitly only to deviate from the inferred tag:

```json
"build": "docker build --platform linux/amd64 -t app:release .",
"artifact": "image:app:release"
```

On the box the container runs under the same generated template unit as any service — rootful podman, foreground, journald logs — with privileges dropped inside the container (`--user` as `run.user`, `--cap-drop=all`). `run.exec` must be absent (the image's CMD/ENTRYPOINT is the command); the sandbox knobs map to container equivalents (`read_write_paths` → bind mounts, `ambient_caps` → `--cap-add`, `env_extra` → `--env`). The env file contract tightens: podman reads `/etc/<name>/env` literally, so values must be unquoted (`hadi env` enforces this). `hadi check` prints the exact unit and which engine holds the tag. Mechanics: [docker.md](docker.md).

- A **binary**: installed to the exec path; each release also kept as a sha-tagged copy for rollback.
- A **release tarball** (`.tgz` / `.tar.gz`): unpacked per deploy to `/opt/<name>/releases/<sha>/`, with a `current` symlink repointed before the new version starts. For Elixir releases and anything that's a directory rather than a file:

```json
"build": "MIX_ENV=prod mix release && tar -C _build/prod/rel -czf dist/app.tgz app",
"artifact": "dist/app.tgz",
"run": {"exec": "bin/app start"}
```

Retention: the last 5 artifacts stay on each box; older ones are pruned on deploy (image prune is ledger-driven, so a rollback target is never evicted). Retention depth equals rollback depth. Rollback refuses to cross an artifact-kind switch — a sha deployed as a tarball can't be restored by an image-era deploy.json; the error tells you to restore that era's config and deploy.

### colors

The two internal ports the service alternates between:

```json
"colors": [8081, 8082]
```

Defaults: port entries get front+1 and front+2; domain entries get 4001 and 4002. Set explicitly only when several hadi services share a box.

### health

HTTP path polled to verify a new version before traffic moves. Default `/healthz`. Make it honest: check your dependencies, not just liveness, because whatever this returns decides whether traffic flips.

### files

Extra files shipped on every deploy, local path to remote path:

```json
"files": {"deploy/compose/sidecar.yml": "/opt/api/sidecar.yml"}
```

### extra_units

A directory of additional systemd units (timers, oneshots, alert hooks) shipped verbatim on every deploy:

```json
"extra_units": "deploy/systemd"
```

A file named `<name>@.service` in there is ignored; hadi generates that one.

## run: the process

hadi generates the service's systemd unit from these knobs. One template in one codebase means the unit on the box can never drift from the repo.

### run.port_env

The environment variable your service reads its listen port from. Default `PORT` — the convention nearly every runtime and framework already follows. Set it only when your service reads a different variable:

```json
"run": {"port_env": "HTTP_PORT"}
```

hadi injects the color's port through it. The env file must not set this variable; `hadi env` refuses to ship one that does. If your service doesn't actually read it, the new color never passes health verification and the deploy aborts with the old version still serving — a loud failure, not a silent one.

### run.user

Unix user the service runs as. Default: the service name. Created by your provisioning, not by hadi.

### run.exec

The command the unit starts. Default `/opt/<name>/bin/<name>`. For release tarballs it's relative to the unpacked release directory. For image artifacts it must be absent — the container runs its image's CMD/ENTRYPOINT, and `hadi check` rejects the combination.

### run.after, run.requires

Extra systemd ordering and hard dependencies:

```json
"run": {"after": ["docker.service"], "requires": ["postgresql.service"]}
```

### run.stop_timeout_sec

How long a draining old version may keep running after traffic moves (long downloads, websocket drains). Default 90.

### run.ready_timeout_sec

How long to wait for a new version to become healthy before giving up and rolling back the start. Default 60. Raise it for slow-booting runtimes.

### run.ambient_caps

Kernel capabilities, when the service genuinely needs them:

```json
"run": {"ambient_caps": ["CAP_NET_BIND_SERVICE", "CAP_NET_ADMIN"]}
```

Setting any capability disables the default `NoNewPrivileges` hardening (they conflict).

### run.read_write_paths

Writable paths under the otherwise read-only filesystem the generated unit enforces:

```json
"run": {"read_write_paths": ["/var/lib/api", "/var/cache/api"]}
```

### run.env_extra

Fixed variables baked into the unit (as opposed to the editable env file):

```json
"run": {"env_extra": {"SHUTDOWN_TIMEOUT": "10m"}}
```

### run.delegate

cgroup controllers to delegate, for services that create their own task cgroups:

```json
"run": {"delegate": ["cpu", "io", "memory", "pids"]}
```

### run.unit_file

Escape hatch: a hand-written unit template that replaces generation entirely. You lose the no-drift guarantee, you keep the freedom.

```json
"run": {"unit_file": "deploy/systemd/custom@.service"}
```

## hooks

Three extension points. The contract: **hooks must be idempotent**, because rerunning a failed deploy reruns them.

### hooks.before_start

Runs on each box after units and files are in place, before the new version starts. The place for sidecar refreshes and timer enables:

```json
"hooks": {"before_start": "docker compose -f /opt/api/sidecar.yml up -d --pull always"}
```

### hooks.once_before_flip

Runs on exactly one box per deploy, after the new version is verified and before any traffic moves. The place for database migrations: a failure aborts the whole deploy with the old version still serving.

```json
"hooks": {"once_before_flip": "bin/app eval 'App.Release.migrate()'"}
```

For **image artifacts** this hook runs where the app lives: inside a one-shot container of the new sha, via `/bin/sh -c` (the image must contain `/bin/sh`; distroless images can't use this hook). Shell semantics (`&&`, `$VARS` from the env file) are preserved; box paths are not reachable — that's the one deliberate difference between kinds.

### hooks.after_flip

Runs on each box after traffic has moved. Failures warn but don't fail the deploy (it's already live).
