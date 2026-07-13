# hadi in CI

The whole idea: your workflow should contain zero knowledge about the service. No build command, no ports, no server list. `deploy.json` knows everything; CI's job is "fetch hadi, run hadi".

## GitHub Actions

The complete workflow:

```yaml
name: deploy
on:
  push:
    branches: [main]
  workflow_dispatch: {}

concurrency:
  group: deploy-myservice
  cancel-in-progress: false   # never interleave two deploys

jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version: "1.25"

      - run: go install github.com/mitteai/hadi@v0.2.0

      - run: $(go env GOPATH)/bin/hadi deploy
        env:
          HADI_SSH_KEY: ${{ secrets.DEPLOY_SSH_KEY }}
```

One secret: the SSH private key for your boxes, pasted as `DEPLOY_SSH_KEY`. That's the entire setup.

Add a check on pull requests so config errors surface before merge:

```yaml
      - run: $(go env GOPATH)/bin/hadi check
```

## Pin the version

Pin a version so upgrades are deliberate. The minimal form:

```yaml
- run: go install github.com/mitteai/hadi@v0.3.0
- run: $(go env GOPATH)/bin/hadi deploy
  env:
    HADI_SSH_KEY: ${{ secrets.DEPLOY_SSH_KEY }}
```

Never `@latest`: a hadi release must not be able to change your deploys until you bump the pin in a reviewable one-line diff. Workstations can ride `hadi update`; CI upgrades deliberately.

## Any other CI

hadi is a static binary with no dependencies, so any runner works. Either install with Go as above, or download a release binary:

```bash
curl -fsSL -o hadi \
  https://github.com/mitteai/hadi/releases/download/v0.2.0/hadi-linux-amd64
chmod +x hadi
HADI_SSH_KEY="$SSH_KEY" ./hadi deploy
```

Verify the download against the release's `sha256sums.txt` if your CI can't trust its network.

## What CI should never do

- **`hadi env`**: environment changes are operator actions from a workstation. Keeping them out of CI means secrets never transit your CI provider, and the deploy key can't read what it doesn't need.
- **`hadi update`**: version pinning exists so upgrades are deliberate.
- **Concurrent deploys**: use your CI's concurrency controls (as above). hadi also takes a per-box lock and fails fast if another deploy holds it, so a race ends in a clear error, not a corrupted flip.

## Failure behavior

`hadi deploy` exits `1` when a deploy fails with the service safe on the previous version, and the failure output includes the new version's journal tail and last health response, so the CI log usually contains the diagnosis. Exit `2` means the config is invalid and nothing was touched. There is nothing to clean up in either case; fix and push again.
