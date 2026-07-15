# Deploying with Docker

Some runtimes don't fit in a single binary: native dependencies, locales, a pinned OS, a pile of pip/gem/hex packages. For those, hadi ships your Docker image as the artifact — and changes nothing else. Same commands, same blue-green flip, same env handling, same rollback. No registry to run or pay for: the image travels over SSH like every other artifact, and the SSH key stays the only credential.

You write a Dockerfile. hadi handles the rest. There is no Docker daemon on your boxes — containers run under podman (daemonless, installed automatically), supervised by the same systemd template unit as any hadi service.

## 1. The service

The same two rules as always — read your port from an env var, answer a health path — plus one more: handle SIGTERM for graceful shutdown (podman forwards it to your process; most frameworks do this out of the box).

```python
# app.py
import os
from flask import Flask

app = Flask(__name__)

@app.get("/")
def hello():
    return os.environ.get("GREETING", "hello from a container") + "\n"

@app.get("/healthz")
def healthz():
    return "ok\n"
```

```dockerfile
# Dockerfile
FROM python:3.12-slim
WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY app.py .
CMD ["sh", "-c", "exec gunicorn -b 127.0.0.1:$PORT app:app"]
```

`$PORT` is injected by the unit, one value per color — that's the blue-green mechanism. Binding `127.0.0.1` is right: the container shares the box's network namespace, and only Caddy needs to reach the color ports.

## 2. The config

```json
{
  "name": "hello",
  "zone": "example.com",
  "hosts": ["203.0.113.10"],
  "build": "docker build --platform linux/amd64 -t hello:release .",
  "artifact": "image:hello:release",
  "run": {"port_env": "PORT"},
  "entry": {"domain": "hello.example.com"}
}
```

Two lines differ from a binary deploy: `build` produces a local image tag instead of a file, and `artifact` names it with the `image:` prefix. The `--platform` flag matters on Apple Silicon — your boxes are amd64.

The build contract is exactly that shallow: hadi never parses your Dockerfile or passes build args; it looks the tag up in your local docker (or podman — whichever holds it) when it's time to ship.

## 3. Check, then deploy

`hadi check` prints the container unit it will install, so nothing is magical:

```bash
$ hadi check
service   hello (zone example.com)
...
artifact  image hello:release (found via docker; ships as save|zstd|load, no registry)
          box tags localhost/hello:<sha> + :current · runs as uid of "hello" via rootful podman
unit      generated hello@.service ({{UID}}/{{GID}} resolve to "hello"'s ids on each box at install):
  ...
  ExecStart=/usr/bin/podman run --rm --replace --name hello-%i \
    --sdnotify=conmon --cgroups=split --pull=never --log-driver=passthrough \
    ...
```

Then:

```bash
$ hadi deploy
saved hello:release via docker (48.2MB zstd) in 2.1s
hello 3f2c91a → 1 box(es) (203.0.113.10)

[203.0.113.10] ensure   caddy + dirs + site (idempotent)               22.0s
[203.0.113.10] ship     image 3f2c91a (48.2MB zstd)                     6.4s
[203.0.113.10] units    hello@.service + 0 extra, daemon-reload         0.4s
[203.0.113.10] start    hello@4001 (idle color)                         0.3s
[203.0.113.10] verify   /healthz on :4001  ok                           0.1s
[203.0.113.10] flip     caddy → :4001                                   0.2s
[203.0.113.10] confirm  /healthz through front door  ok                 0.1s
[203.0.113.10] drain    hello@4002 (≤90s, non-blocking)

deployed 3f2c91a in 31.5s · rollback: hadi rollback
```

The first `ensure` installs podman alongside Caddy — slow once, then never again. What `ship` did: `docker save | zstd` locally, streamed the file over SSH, `podman load` on the box, tagged it `localhost/hello:3f2c91a` and `localhost/hello:current`. The `:current` tag is the image world's `current` symlink — the unit always runs `:current`, and deploys and rollbacks just move the tag.

## 4. Everything still works

```bash
hadi logs -f                   # container stdout/stderr, via journald, as always
hadi env set GREETING=hi       # same box env file, handed to the container; flips, zero downtime
hadi status                    # live color, sha, health
hadi releases                  # the ledger now shows each release's kind
hadi rollback                  # retags :current to the previous sha — no network, seconds
hadi ssh                       # the box; `podman exec hello-4001 sh` gets you inside the container
```

One contract tightens: podman reads `/etc/<name>/env` literally, so values must be unquoted. `FOO=a b` is fine; `FOO="a b"` would put actual quotes in the value, and `hadi env` refuses it with an explanation.

## 5. Migrations

`once_before_flip` runs **inside a one-shot container** of the new sha — where your app and its runtime live — after health verification, before any traffic moves:

```json
"hooks": {"once_before_flip": "python manage.py migrate"}
```

It runs via `/bin/sh -c`, so `&&` chains and env vars work; the image must contain `/bin/sh` (distroless images can't use this hook). It can't touch box paths — it's in the container. A failed migration aborts the deploy with the old version still serving, same as ever.

## 6. What runs on the box

Fair questions, short answers:

- **Is there a Docker daemon now?** No. podman is daemonless: the container is a direct child of the systemd unit, in the unit's cgroup. `systemctl stop` delivers SIGTERM to your process; `ps` shows it; journald has its logs.
- **Root?** The podman launcher runs as root (that's what lets hadi orchestrate everything over root SSH with one storage); **your process does not** — it runs as `run.user`'s uid with all capabilities dropped, and the container sees no host filesystem beyond what `read_write_paths` mounts.
- **Can a box pull from a registry?** No. Tags are `localhost/`-prefixed and the unit passes `--pull=never`. If it isn't on the box, it doesn't run.
- **Disk?** The last 5 deployed images stay (that's your rollback depth); older ones and dangling layers are pruned on each deploy.

## Gotchas

- **Write paths need `run.read_write_paths`** — they become bind mounts, and the files belong to `run.user` on the box, matching the uid inside.
- **The image's `USER` is overridden** by `run.user`'s uid. Don't rely on a specific in-container username existing.
- **Tag in both engines?** If `docker` and `podman` both hold your tag with different image IDs, hadi refuses rather than guess. Remove the stale one.
- **Switching an existing service to images** (or back) deploys cleanly, but rollback won't cross the switch: a sha deployed as a binary can't be restored by an image-era deploy.json. The error tells you what to do (restore that era's deploy.json and deploy).
- **Local requirements:** the engine you build with, plus `zstd`.

## Where next

- [config.md](config.md): the `image:` artifact and every other option
- [quick-start.md](quick-start.md): the same walkthrough for plain binaries
- [how-it-works.md](how-it-works.md): the design decisions behind image artifacts
- [troubleshooting.md](troubleshooting.md): the image-specific error messages, decoded
