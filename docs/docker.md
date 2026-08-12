# Image deploys, in depth

Containers are hadi's default artifact: a Dockerfile next to deploy.json is all it takes ([quick-start.md](quick-start.md) is the walkthrough). This page is the mechanics underneath — what the config forms mean, what actually runs on the box, and the contracts that differ from a plain binary.

The shape stays hadi's: same commands, same blue-green flip, same env handling, same rollback. No registry to run or pay for — the image travels over SSH like every other artifact, and the SSH key stays the only credential. No Docker daemon on your boxes — containers run under podman (daemonless, installed automatically), supervised by the same systemd template unit as any hadi service.

## The config, two forms

**Inferred (the default).** `build` and `artifact` both absent plus a `Dockerfile` next to deploy.json:

```json
{
  "name": "hello",
  "zone": "example.com",
  "entry": {"domain": "hello.example.com"}
}
```

hadi fills in `build: docker build --platform linux/amd64 -t hello:hadi .` (podman if docker isn't installed) and `artifact: image:hello:hadi`. `hadi check` prints both with a `(default: Dockerfile found)` label. The `hello:hadi` tag is a local scratch tag — boxes only ever see `localhost/` tags.

**Explicit (any deviation).** Your own build command and tag:

```json
"build": "docker build --platform linux/amd64 --build-arg REV=$(git rev-parse HEAD) -t hello:release .",
"artifact": "image:hello:release"
```

Explicit always wins — a Dockerfile in the repo changes nothing once `build` or `artifact` is stated (check prints a one-line note that the Dockerfile is unused). The `--platform` flag matters on Apple Silicon: your boxes are amd64. The inferred form bakes it in; the explicit form is your responsibility.

Either way, the build contract is exactly this shallow: hadi never parses your Dockerfile or passes build args; it runs `build`, then looks the tag up in your local docker (or podman — whichever holds it) when it's time to ship.

## The container's three rules

Same two rules as any hadi service — read your port from the port env var (`PORT` by default), answer a health path — plus one: handle SIGTERM for graceful shutdown (podman forwards it to your process; most frameworks do this out of the box). Bind `127.0.0.1`: the container shares the box's network namespace, and only Caddy needs to reach the color ports.

## What ship does

`docker save | zstd` locally (once per deploy), stream the file over SSH, `podman load` on the box, tag `localhost/<name>:<sha>` plus a moving `localhost/<name>:current`. The `:current` tag is the image world's `current` symlink — the unit always runs `:current`, and deploys and rollbacks just move the tag. A rollback is a retag of an image already on disk: no network, seconds.

The first `ensure` installs podman alongside Caddy — slow once, then never again.

## Everything still works

```bash
hadi logs -f                   # container stdout/stderr, via journald, as always
hadi env set GREETING=hi       # same box env file, handed to the container; flips, zero downtime
hadi status                    # live color, sha, health
hadi releases                  # the ledger shows each release's kind
hadi rollback                  # retags :current to the previous sha
hadi ssh                       # the box; `podman exec hello-4001 sh` gets you inside the container
```

One contract tightens: podman reads `/etc/<name>/env` literally, so values must be unquoted. `FOO=a b` is fine; `FOO="a b"` would put actual quotes in the value, and `hadi env` refuses it with an explanation.

## Migrations

`once_before_flip` runs **inside a one-shot container** of the new sha — where your app and its runtime live — after health verification, before any traffic moves:

```json
"hooks": {"once_before_flip": "python manage.py migrate"}
```

It runs via `/bin/sh -c`, so `&&` chains and env vars work; the image must contain `/bin/sh` (distroless images can't use this hook). It can't touch box paths — it's in the container. A failed migration aborts the deploy with the old version still serving, same as ever.

## What runs on the box

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

- [quick-start.md](quick-start.md): the end-to-end walkthrough — Dockerfile to production
- [no-docker.md](no-docker.md): shipping a plain binary instead
- [config.md](config.md): the `image:` artifact and every other option
- [how-it-works.md](how-it-works.md): the design decisions behind image artifacts
- [troubleshooting.md](troubleshooting.md): the image-specific error messages, decoded
