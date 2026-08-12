# Proposal: Docker by default

**Status:** implemented (branch `docker-by-default`) · proposed 2026-08-12

## Summary

Make the Dockerfile the paved road. When `deploy.json` omits both `build` and `artifact` and a `Dockerfile` sits next to it, hadi infers the two lines it needs:

```
build:    docker build --platform linux/amd64 -t <name>:hadi .
artifact: image:<name>:hadi
```

The smallest dockerized config drops to:

```json
{
  "name": "forms",
  "zone": "example.com",
  "entry": {"domain": "forms.example.com"}
}
```

Everything else is documentation: the quick start becomes the Dockerfile walkthrough, and the binary path becomes the documented opt-out — stated explicitly via `build`/`artifact`, exactly as it is written today. No new commands, no new flags, no new config keys, no mode switch. One inference rule and a docs flip.

## Motivation

Docker is the default shape of a project now. Most services arrive with a Dockerfile already in the repo — it is how the team runs the app locally, how CI builds it, how new hires understand the runtime. For those projects, today's hadi asks them to restate what the Dockerfile already declares:

```json
"build": "docker build --platform linux/amd64 -t forms:release .",
"artifact": "image:forms:release"
```

Both lines are derivable. The repo has already answered "how does this build" (the Dockerfile) and "what is it called" (`name`, which hadi already owns for paths, units, and tags). A config that restates them isn't stating a deviation — it's boilerplate, and our own rule for `deploy.json` is that *a config states only what deviates*.

The current docs also frame containers as the fallback for "messy runtimes" — native deps, pinned OSes, piles of packages. That framing is inverted from how most teams experience it: the Dockerfile is what they have, and the single static binary is the special case. The tool already treats both kinds identically at runtime; the docs and defaults should meet users where they are.

## What this is not

Naming the non-goals first, because "Docker support" usually smuggles in infrastructure, and the entire point is that ours doesn't:

- **No registry. Ever.** Images still travel `save | zstd | ssh | load`. The SSH key stays the only credential. This proposal changes which path is paved, not the bets under it.
- **No Docker daemon on the boxes.** Podman remains the runtime, daemonless, converged by `ensure`, supervised by the same generated systemd unit. "Docker by default" refers to the developer's side — their Dockerfile, their local `docker build`.
- **No compose, no accessories, no buildpacks.** Databases and sidecars remain provisioning's job. hadi begins where a running box ends.
- **No Dockerfile parsing.** The build contract stays exactly as shallow as it is: hadi runs a build command and looks up a tag. It never reads the Dockerfile's contents, passes build args, or infers ports from `EXPOSE`.
- **No local Docker requirement for binary users.** The inference fires only when a Dockerfile exists. A repo without one never touches a container engine.

## Why the change is this small

The hard work already shipped. Image artifacts aren't a bolted-on mode — they run under the same generated template unit, the same blue-green flip, the same journald, with the moving `localhost/<name>:current` tag playing the `current` symlink's role. Consequently every command is already kind-agnostic, which is exactly the "everything just works" bar this proposal must meet:

| Command | With an image artifact today |
|---|---|
| `hadi deploy` | same 11-step lifecycle; `ship` is save/stream/load instead of copy |
| `hadi env` (edit/set/unset/push/pull) | same `/etc/<name>/env` file, handed to the container via `--env-file`, applied with the same health-gated flip |
| `hadi logs -f` | same journald — the container is a child of the unit, `--log-driver=passthrough` |
| `hadi top` | same units, same journals, same vitals |
| `hadi status` / `releases` / `rollback` | same ledger; rollback is a retag of an image already on disk, seconds, no network |
| `hadi ssh` / `exec` | same box semantics in both worlds |
| `hadi rm` | already removes `localhost/<name>` images alongside everything else |

There is no parity gap to close. What remains is a default, a `check` line, and a documentation inversion.

## Design

### The inference rule

In `config.Load`, after validation, before `ApplyDefaults`:

> If `build` **and** `artifact` are both absent, and a file named `Dockerfile` exists in the same directory as `deploy.json`, set `build` to `docker build --platform linux/amd64 -t <name>:hadi .` and `artifact` to `image:<name>:hadi`.

The precise edges, each chosen for the least surprise:

- **Both keys absent, or no inference.** An explicit `artifact` (or `build`) always wins, even with a Dockerfile in the repo. Adding a Dockerfile to a binary-configured service changes nothing — the only way to switch kinds is to edit `deploy.json`, a deliberate act, and the existing kind-switch guards (clean one-way deploy, rollback refuses across the boundary with instructions) already govern that transition.
- **Backward compatible by construction.** Today a config with `artifact` absent is broken — deploy fails reading an empty path. No working config exists whose meaning this changes. As a side benefit, the currently-mushy failure becomes a crisp exit-2 validation error.
- **No Dockerfile and no artifact** is that crisp error, and it teaches both roads:

  ```
  deploy.json invalid:
    artifact: nothing to ship — add a Dockerfile next to deploy.json
    (hadi will build and deploy it), or set "build" and "artifact"
    to ship a binary or release tarball
  ```

- **`Dockerfile` exactly, next to `deploy.json`.** No walking up directories, no `-f` variants, no `dockerfile:` config key to point elsewhere. A monorepo or a nonstandard filename states `build`/`artifact` explicitly — which it must today anyway. If real demand for a knob emerges, it can be added later; starting without one is the reversible choice.
- **The tag is `<name>:hadi`.** Deterministic, derived from the name hadi already owns, and namespaced so it can never collide with tags the user's own workflows produce (`:release`, `:latest`). It is a local scratch tag: the box never sees it — boxes only ever hold `localhost/<name>:<sha>` and `:current`, unchanged.
- **`--platform linux/amd64` is baked in.** Boxes are amd64; the default should produce a deployable image from an Apple Silicon laptop without the user learning that flag from a failed health check. (This erases the walkthrough's current top gotcha for everyone on the default path.)
- **Engine: `docker` if on `PATH`, else `podman`.** The ship step already looks the tag up in either engine; the build default follows the same tolerance. The existing both-engines-hold-the-tag refusal stays.

### Nothing magical: `check` shows its work

Per the philosophy that `hadi check` prints the plan so nothing is hidden, inferred values are printed with their provenance:

```
$ hadi check
service   forms (zone example.com)
build     docker build --platform linux/amd64 -t forms:hadi .   (default: Dockerfile found)
artifact  image:forms:hadi                                      (default: Dockerfile found)
...
```

And the inverse case gets one informational line, so an ignored Dockerfile is never a silent mystery:

```
note      Dockerfile present but unused: artifact is set to bin/forms-linux
```

That one line is the entire "which mode am I in" surface. There is no mode — there is a config, fully printed.

### Companion change (separable): default `run.port_env` to `"PORT"`

`run.port_env` is required today. `PORT` is the ecosystem-wide convention — Heroku, Cloud Run, every 12-factor template, most framework scaffolds. Defaulting it to `PORT` takes the minimal config from four keys to three (`name`, `zone`, `entry`).

This is safe in hadi's model: if the app doesn't actually read `PORT`, the new color never passes health verification, the deploy aborts, and the old version keeps serving — a loud, safe failure with the journal tail printed. The existing guard (refusing an env file that sets the port variable) applies to the default exactly as it does to an explicit value.

Separable: accept or reject independently of the inference rule. Recommended: accept — "required" was doing the work of "there is no sane default," and now there is one.

### The docs flip

The code change is small; the positioning change is the point. Docker becomes the front door, binaries become a first-class documented choice:

- **README quick start** shows a repo with a Dockerfile and the three-line config, then: *"No Dockerfile? Ship a binary or a release tarball instead — same commands, same flip: [docs/no-docker.md]."* The current "messy runtime" paragraph inverts into "already have a Dockerfile? then you're done."
- **`docs/quick-start.md`** becomes the Dockerfile walkthrough (today's `docker.md` content, trimmed of its opt-in framing). The port/health/SIGTERM contract, `check`, deploy output, env/logs/rollback tour — all as today.
- **`docs/no-docker.md`** (today's quick-start) carries the binary and tarball walkthrough. The title says what it is; the content barely changes.
- **`docs/config.md`** reorders the `artifact` kinds — image first with "usually inferred; you only write this to deviate," binary and tarball as the explicit forms — and documents the inference rule in one paragraph.
- **`docs/why-hadi.md`** stays honest with one adjustment: the Kamal section's "hadi deliberately keeps it optional" becomes "hadi makes the container the default artifact while keeping the container *ecosystem* — registry, daemon, image-only lock-in — optional." The scorecard's "podman opt-in" cell becomes "podman for image services." The bets are untouched; the comparison must not oversell the change.

### Considered and rejected

- **`"artifact": "image"` shorthand** (explicit marker, inferred tag and build). Honest, but it keeps one line of boilerplate whose only content is "yes, the Dockerfile you can see is real." The file's presence is the declaration; `check` printing the inference covers the explicitness need.
- **A `hadi init` scaffold.** A three-key JSON needs no generator, and hadi has no template state to own.
- **Per-kind `exec` semantics** (auto-entering the container for image services). `exec` means "on the box, as root" in both worlds; making its meaning depend on the artifact kind is exactly the modal behavior the philosophy avoids. Getting inside the container stays one documented step away: `hadi ssh`, then `podman exec <name>-<port> sh`.
- **Requiring a flag for the binary path.** Hostile and unnecessary. Explicit config *is* the opt-out, and it is zero migration for every existing user.
- **Registry support to feel "more native."** Violates the core bet. A registry is a second credential, a bill, and an availability dependency in the 2am deploy path.

## The UX, compared: is hadi actually simpler?

The claim behind this proposal is a UX claim, so it should survive contact with the competition. [why-hadi.md](../why-hadi.md) compares architectures; this section compares what a person actually types and reads, across the four moments that make up real life with a deploy tool. Same honest frame: Dokku and Kamal are good at what they bet on.

### Moment 1: first deploy of a dockerized app

**Dokku** — install the platform on the server, then configure server-side:

```bash
# on the box:
wget -qO- https://dokku.com/install/...bootstrap.sh | sudo bash
dokku apps:create forms
dokku domains:set forms forms.example.com
sudo dokku plugin:install .../dokku-letsencrypt
dokku letsencrypt:enable forms
# locally:
git remote add dokku dokku@box:forms
git push dokku main
```

The `git push` at the end is the best day-one moment in the industry — but it is the last step of a server-side installation, and the box now runs a platform you upgrade.

**Kamal** — install the gem, then fill in `config/deploy.yml`: service, image name, server IPs, builder arch, proxy host, and a registry — which means creating a registry account somewhere, putting its password in `.kamal/secrets`, and picking a secrets adapter. Then `kamal setup`. The config is honest and readable, but its minimum is ~15 lines, and two of its concepts (registry, builder) exist for the tool, not for your app.

**hadi, with this proposal** — install the client, then:

```json
{"name": "forms", "zone": "example.com", "entry": {"domain": "forms.example.com"}}
```

```bash
hadi check && hadi deploy
```

No server-side install (the box needs only SSH; `ensure` converges the rest), no registry, no secrets adapter, no builder config. This proposal is precisely what wins this moment: before it, hadi's dockerized config was comparable to Kamal's minus the registry; after it, there is nothing left to write that the repo hasn't already said.

The caveat that keeps this honest: hadi assumes a provisioned box (a running Linux with the service user created) and either DNS records or a `hosts` line. Dokku creates its world for you; Kamal installs Docker for you on `setup`. hadi deliberately doesn't provision — that's terraform/cloud-init's job — so "first deploy" is simplest *given* the box, and getting the box is homework the other two partially do for you.

### Moment 2: change an env var

- **hadi**: `hadi env set -s forms STRIPE_KEY=sk_live_x` — one command; the new value boots the idle color, must pass health checks, then traffic flips. A broken value cannot take the service down.
- **Dokku**: `dokku config:set forms STRIPE_KEY=sk_live_x` — one command (run on the server), restarts the app through its deploy cycle. Genuinely comparable UX.
- **Kamal**: edit `config/deploy.yml` (clear values) or `.kamal/secrets` (secrets), then `kamal deploy` or `kamal app boot`. Config-as-file has upsides (it's in git), but the everyday gesture is edit-two-places-then-redeploy rather than one command.

### Moment 3: logs and rollback

Logs are a wash — `hadi logs -f`, `kamal app logs -f`, `dokku logs forms -t` are the same gesture. (`hadi top`, the live fleet dashboard, has no equivalent in either.)

Rollback is not a wash:

- **hadi**: `hadi rollback` — the artifact is already on the box (retention = rollback depth), so it's a retag/reinstall plus the same health-gated flip. Seconds, no network, and the ledger (`hadi releases`) tells you what you're rolling to.
- **Kamal**: `kamal rollback <version>` — you first find the version hash (`kamal audit`), and the image must still be on the host or the registry must be reachable. Usually fine; one more dependency in the worst moment.
- **Dokku**: no rollback command. You re-push an old commit and rebuild — on the production box.

### Moment 4: 2am, something is wrong

This is where the architectures become UX:

- **hadi**: the debugging surface is a Linux box. `ps` shows your process, `journalctl` has its logs, `/etc/forms/env` is its config, one Caddy file says where traffic goes. Every skill transfers in from twenty years of Linux and transfers out to everything else you run.
- **Kamal**: the surface is the Docker ecosystem — `docker ps`, container logs, kamal-proxy's state — plus the registry, which must be up to ship the fix.
- **Dokku**: the surface is the platform — its CLI, its nginx config, its plugin state. Excellent while it works; when the platform itself misbehaves, you debug Dokku before you debug your app.

### The verdict, in three parts

**Is hadi simpler?** At the surface, yes — measurably, after this proposal. Smallest config (3 keys vs ~15 lines vs a server-side setup), fewest concepts that exist for the tool rather than the app (zero: no registry, no builder, no buildpack, no plugin), fewest credentials (one), and nothing on the box that is *of* the deploy tool. Day-two commands are one-liners across all three tools; hadi's carry the health-gated flip everywhere, including env changes and rollbacks.

**Is it more intuitive?** Intuitive means "matches a prior you already have," so it depends which prior. Dokku is intuitive if your prior is Heroku. Kamal is intuitive if your prior is the Docker ecosystem end-to-end. hadi is intuitive if your prior is Linux — and after this proposal, its front door (a Dockerfile and `docker build`) matches the Docker prior too, while the box stays on the Linux prior. That's the strongest position of the three: the *familiar* interface on the way in, the *durable* one underneath.

**Where is hadi more complicated?** At the edges it refuses to own, and it's fair to name them: you provision boxes and users yourself (the others do some of it for you); DNS-as-registry is a concept neither competitor asks you to learn (though `hosts` opts out); and there are no accessories — `dokku postgres:create` and Kamal's `accessory` have no hadi equivalent, ever, because databases belong to provisioning. That's complexity moved out of the tool but not out of your world. The trade is deliberate: Dokku and Kamal spend your complexity budget on platform surface you operate forever; hadi spends it once, at provisioning time, on artifacts you'd want anyway (terraform you can read, boxes you can log into).

So the honest one-liner: **hadi has the smallest everyday surface and the most legible failure mode, at the cost of doing your own provisioning; Dokku beats it on day-zero magic for one box; Kamal matches it on fleet workflow but charges a registry, a daemon, and ~5× the config for the privilege.** This proposal exists to remove the one place that sentence used to hedge — the dockerized day-one config.

## Implementation sketch

Small, mostly in one file:

- `internal/config/config.go`: inference in `Load` (it knows the config path, so the Dockerfile sibling check is a one-line `os.Stat`), the new validation message, provenance tracking (two booleans or a `Defaulted` field) for `check` to print. With the companion change, `run.port_env` moves from `Validate` to `ApplyDefaults`. ~40 lines.
- `check` output: the two provenance-labeled lines and the unused-Dockerfile note. ~10 lines.
- `deploy.go`, `engine.go`, `env.go`, `top.go`, `rm.go`: **no changes.** They receive a config with `build`/`artifact` filled, same as ever. `--skip-build` already does the right thing (ships the existing local tag).
- Tests: `config_test.go` table over {Dockerfile present/absent} × {build/artifact present/absent}, the error text, precedence of explicit keys, and the port_env default.
- Docs: the flip described above, plus `man/hadi.1` examples.

Two PRs: (1) inference + check + tests, (2) the docs flip. Shippable independently; the docs PR lands second so it documents released behavior.

## Risks

- **Implicitness vs. "nothing magical."** The real risk of any inference. Contained by scope (a single file-existence check, no parsing, no directory walking) and by `check` printing every inferred value with its provenance. The magic is one `os.Stat`, and it is disclosed.
- **Accidental kind switches.** Cannot happen from adding a Dockerfile (explicit config wins). Can only happen by deleting `build`/`artifact` from a config in a repo with a Dockerfile — a deliberate edit, after which the existing cross-kind rollback refusal still protects the history, with instructions.
- **Default build command too rigid** (build args, `-f` paths, multi-stage targets). By design: the default covers the common case; every deviation is one explicit `build` line away, which is today's status quo. We are not worse anywhere; we are three lines better in the common case.
- **`--platform` support in old local engines.** Requires BuildKit (standard since Docker 20.10, default since 23) or any recent podman. Anyone on an engine too old for `--platform` writes their own `build` line — the opt-out is uniform.

## The criteria, checked

- **Simple / low complexity:** one inference rule, ~50 lines of Go, zero new commands, flags, keys, or box-side changes.
- **Intuitive:** a repo with a Dockerfile deploys with a three-key config; the tool does what the repo's presence of a Dockerfile already implies.
- **Familiar:** the developer interface is `docker build` and a Dockerfile — what every team already knows. The `PORT` convention is the industry's.
- **Everything just works, both worlds:** already true by construction (one lifecycle, one unit template, one env file, one journal), verified by the parity table above; this proposal adds no seam that could break it.
- **Minimalist:** the no-goals list is longer than the feature. Nothing new runs on the boxes, and nothing new must be operated, paid for, or secured.
