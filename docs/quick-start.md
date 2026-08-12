# Quick start: Dockerfile to production

Deploy a dockerized service to a real box, with HTTPS, in about five minutes. You'll need one Linux box (Debian-family, root SSH), a domain you control, and Docker (or podman) locally — which you have, because your repo has a Dockerfile.

There is no registry in what follows, and no Docker daemon ends up on your box: hadi builds locally, streams the image over SSH, and runs it under daemonless podman, supervised by systemd like any other service.

## 1. The service

Three rules make a container hadi-deployable: read your port from the `PORT` env var, answer a health path, and handle SIGTERM for graceful shutdown (most frameworks do out of the box).

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

`$PORT` is injected by the generated unit, one value per color — that's the blue-green mechanism. Binding `127.0.0.1` is right: the container shares the box's network namespace, and only Caddy needs to reach the color ports.

## 2. The config

```json
{
  "name": "hello",
  "zone": "example.com",
  "hosts": ["203.0.113.10"],
  "entry": {"domain": "hello.example.com"}
}
```

That's the whole deployment surface. There is no build or artifact line because the Dockerfile next to this file *is* the build declaration: hadi fills in `docker build --platform linux/amd64 -t hello:hadi .` and ships the resulting image. Write `build`/`artifact` yourself only to deviate — a custom tag, build args, or [a plain binary instead](no-docker.md).

`hosts` points straight at your box (swap in DNS discovery later); `entry.domain` means public HTTPS with automatic certificates.

## 3. The box

Two things hadi won't do for you, because machines are your provisioning's job:

```bash
# an A record: hello.example.com → 203.0.113.10  (at your DNS host, DNS-only)

# the service user, on the box:
ssh root@203.0.113.10 'useradd --system --create-home --home /opt/hello --shell /usr/sbin/nologin hello'
```

## 4. Check, then deploy

`hadi check` prints the plan, including what it inferred — nothing is magical:

```bash
$ hadi check
service   hello (zone example.com)
entry     https://hello.example.com (caddy terminates TLS, auto-renewed)
colors    4001 / 4002   health /healthz   ready_timeout 60s   stop_timeout 90s
build     docker build --platform linux/amd64 -t hello:hadi .   (default: Dockerfile found)
artifact  image hello:hadi (found via docker; ships as save|zstd|load, no registry)
          box tags localhost/hello:<sha> + :current · runs as uid of "hello" via rootful podman
boxes     203.0.113.10 (static hosts)
unit      generated hello@.service ...
ok

$ hadi deploy
saved hello:hadi via docker (48.2MB zstd) in 2.1s
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

The first `ensure` installs Caddy and podman — slow once, then never again. What `ship` did: `docker save | zstd` locally, streamed the file over SSH, `podman load` on the box, tagged it `localhost/hello:3f2c91a` and `localhost/hello:current`. The `:current` tag is the image world's `current` symlink — deploys and rollbacks just move it.

```bash
$ curl https://hello.example.com
hello from a container
```

Live, with a certificate you never think about again.

## 5. Ship a change, watch it flip

Edit the greeting in app.py, commit, and:

```bash
$ hadi deploy
...
[203.0.113.10] start    hello@4002 (idle color)
[203.0.113.10] flip     caddy → :4002
```

Note the ports: the new version came up on 4002 while 4001 kept serving, and traffic moved only after 4002 proved healthy. Run `while true; do curl -s https://hello.example.com; sleep 0.2; done` in another terminal during the deploy; you won't catch a single error.

## 6. Play with the rest

```bash
hadi status                    # live color, sha, health
hadi env set GREETING=hi       # config change, zero downtime
curl https://hello.example.com # → hi
hadi logs -f                   # container stdout/stderr, via journald
hadi rollback                  # retags :current to the previous sha — seconds, no network
```

Every one of these is health-gated the same way the deploy was: nothing replaces a working version without proving itself first. One container-specific contract: env values must be unquoted (`FOO=a b`, not `FOO="a b"`) — podman reads the env file literally, and `hadi env` refuses quoted values with an explanation.

## Migrations

`once_before_flip` runs **inside a one-shot container** of the new sha — where your app and its runtime live — after health verification, before any traffic moves:

```json
"hooks": {"once_before_flip": "python manage.py migrate"}
```

A failed migration aborts the deploy with the old version still serving.

## Where next

- [docker.md](docker.md): what actually runs on the box — units, rootful podman, prune, the gotchas
- [no-docker.md](no-docker.md): the same walkthrough for a plain binary, no container involved
- [config.md](config.md): every deploy.json option (hardening, hooks, release tarballs, sidecars)
- [ci.md](ci.md): auto-deploy on push with one secret
- [dns.md](dns.md): swap `hosts` for DNS discovery so scaling needs no config edits
- [troubleshooting.md](troubleshooting.md): when something doesn't go like this page
