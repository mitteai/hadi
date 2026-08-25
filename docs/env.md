# Environment

The box is the source of truth for a service's environment; hadi is the courier. The file is `/etc/<name>/env` on the box — mode `0640`, owned by the service's `run.user` — and there is deliberately no local template, no encrypted copy in the repo, no vault integration. What the box has is what runs, `hadi env pull` shows it to you, and every mutation goes through the same verify-then-flip machinery as a code deploy.

## Every change is a deploy

`set`, `unset`, `edit`, and `push` all end the same way: hadi ships the new file to the box, boots the idle color under it, polls the health endpoint, and only then moves traffic. A value that breaks boot fails verification, the old color never stops serving, and the old env file has already been replaced on disk — but the process that matters is still running under the values it started with. Fix the value and ship again.

The flip is recorded in the release ledger as `env-change`, so `hadi releases` and `hadi status` show env mutations alongside code deploys.

```bash
hadi env set -s api STRIPE_KEY=sk_live_xxx   # rotate one secret
hadi env unset -s api OLD_FLAG               # remove a value
hadi env edit -s api                         # $EDITOR; save ships + flips, abort does nothing
hadi env pull -s api > api.env               # snapshot before risky work
hadi env push -s api api.env                 # full replace from a file. Never a merge.
```

## Verbs

| Verb | What it does | Example |
|---|---|---|
| `set KEY=VALUE...` | Patch values in place, append when new, then flip. Values may contain `=`; multiple pairs ship as one flip. | `hadi env set -s api TOKEN=a=b== DEBUG=1` |
| `unset KEY...` | Remove keys, then flip. | `hadi env unset -s api OLD_FLAG` |
| `edit` | Pull into `$EDITOR`; save ships + flips, an unchanged file ships nothing. | `hadi env edit -s api` |
| `pull [file]` | Fetch to stdout or a file. Reads every box and warns when they differ. | `hadi env pull -s api > api.env` |
| `push <file>` | Replace the entire env from a local file, then flip. **Never a merge** — keys absent from the file are gone from the box. | `hadi env push -s api api.env` |

Prefer `set`/`unset` for everything routine: they pull the current file first, patch only the named keys, and ship the result, so a value someone else added last week survives your change. `push` is for restoring a known-good snapshot you took with `pull`, or seeding a brand-new fleet — take a `pull` first, always, because push is the one verb that can silently destroy keys.

## Guardrails

- **The port key is refused.** hadi will not ship an env that sets `run.port_env` (`PORT` by default): the systemd unit injects the per-color port, and a file value would override it and break blue-green. This check runs on every verb, including `edit` and `push`.
- **Image services get a syntax lint.** systemd's `EnvironmentFile` strips quotes and honors backslash continuations; podman's `--env-file` takes lines literally. To keep values identical across the kinds, image envs must be literal `KEY=VALUE` lines: no quoted values, no trailing `\`. Unquoted spaces are fine — both parsers take everything after the first `=`.
- **Drift is announced, then resolved in one direction.** Every verb that reads first (`set`, `unset`, `edit`, `pull`) reads all boxes and warns when they differ. The patch is applied to the *first* box's content and shipped to every box — so a `set` on a drifted fleet also realigns the stragglers to box one's view. If a deviant box held something you need, `pull --host <addr>` it before you ship.
- **One mutation at a time.** Shipping takes the same per-box lock as `deploy` and `rollback`, so a concurrent deploy can't interleave with an env change.

## What rollback does not do

The env file is not versioned. `hadi rollback` restores an earlier *artifact* and flips; it does not touch `/etc/<name>/env`. The way to undo an env mistake is another `env set`/`env unset` with the old value — which is why `pull` before risky work is in the examples above. If you don't have the old value, nothing on the box does either.

## Resolving the service

`env` follows the same rules as every service command, and all three requirements must hold before anything is read or shipped:

1. **A service.** Inside a service repo, `./deploy.json` is authoritative and `-s` is unnecessary. Anywhere else, `-s <name>` is required.
2. **A zone**, when using `-s`. Precedence: `--zone <zone>`, then the `zone` of a `./deploy.json` if you happen to be standing in one, then `HADI_ZONE`. With none of the three you get exit 2 and `-s needs a zone` — nothing was touched.
3. **A key.** `--ssh-key <path>`, then `HADI_SSH_KEY` (contents or path), then `~/.ssh/id_ed25519`, then `~/.ssh/id_rsa`.

The practical failure mode: running `hadi env set -s api KEY=value` from a random directory with `HADI_ZONE` unset fails before any SSH happens. Export `HADI_ZONE` (and `HADI_SSH_KEY`) in your shell profile once and `-s` works from anywhere:

```bash
export HADI_ZONE=example.com
export HADI_SSH_KEY=~/.ssh/deploy_key
```

`--host <addr>` restricts any env verb to a single box, bypassing discovery — useful for inspecting one deviant box, and dangerous for mutations on a fleet, since it manufactures exactly the drift the warning exists for.

## Exit codes

Same contract as the rest of hadi: `0` shipped and flipped everywhere · `1` the change failed but the old version is still serving · `2` usage or resolution error, nothing touched.
