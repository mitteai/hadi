# deploy.json

One file per service repo, at the root. Only `name`, `zone`, `entry`, and `run.port_env` are required; everything else has a default, so a config states only what deviates.

The smallest real config:

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

Omit it to always deploy a pre-built artifact.

### artifact

What gets shipped. Two kinds, detected by extension:

- A **binary** (anything else): installed to the exec path; each release also kept as a sha-tagged copy for rollback.
- A **release tarball** (`.tgz` / `.tar.gz`): unpacked per deploy to `/opt/<name>/releases/<sha>/`, with a `current` symlink repointed before the new version starts. For Elixir releases and anything that's a directory rather than a file:

```json
"build": "MIX_ENV=prod mix release && tar -C _build/prod/rel -czf dist/app.tgz app",
"artifact": "dist/app.tgz",
"run": {"exec": "bin/app start", "port_env": "PORT"}
```

Retention: the last 5 artifacts stay on each box; older ones are pruned on deploy. Retention depth equals rollback depth.

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

### run.port_env (required)

The environment variable your service reads its listen port from:

```json
"run": {"port_env": "PORT"}
```

hadi injects the color's port through it. The env file must not set this variable; `hadi env` refuses to ship one that does.

### run.user

Unix user the service runs as. Default: the service name. Created by your provisioning, not by hadi.

### run.exec

The command the unit starts. Default `/opt/<name>/bin/<name>`. For release tarballs it's relative to the unpacked release directory.

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

### hooks.after_flip

Runs on each box after traffic has moved. Failures warn but don't fail the deploy (it's already live).
