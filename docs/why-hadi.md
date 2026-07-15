# Why hadi

Every tool in this space answers the same question — how do I ship code to servers I own, with zero downtime, without hiring a platform team — and each answer is a bet on what the unit of deployment should be. Kamal bets on container images. Dokku bets on a Heroku-shaped platform. Kubernetes bets on a cluster abstraction over your machines. hadi bets on the thing that was already there: **a systemd service on a box you can read.**

That one bet drives everything else:

- **The artifact is whatever your runtime actually needs.** A static binary ships as a binary. An Elixir release ships as a tarball. A runtime with native deps and a pinned OS ships as a container image. Same commands, same flip, no tool switch when a service's needs change.
- **The only credential is an SSH key.** No registry auth, no cluster kubeconfig, no platform API token. Whoever holds the key can deploy; nobody else can. CI is one secret.
- **Nothing to operate between deploys.** hadi is a client. It leaves systemd supervising, Caddy proxying, and journald logging — software your distro already ships and you already trust. There is no agent to upgrade, no daemon to secure, no control plane to page you.
- **Boxes stay legible.** Your process is in `ps`. Its logs are in `journalctl`. Its config is one file in `/etc`. When something breaks at 2am, the debugging surface is a Linux box, not a platform on top of one.

The honest frame for the comparisons below: these tools are all good at what they bet on. The question is whether you need what they charge for.

## hadi vs Kamal

[Kamal](https://kamal-deploy.org) (37signals) is the closest cousin: deploy to your own servers, blue-green behind a proxy, no Kubernetes. The disagreement is the unit of deployment — Kamal ships container images; hadi ships artifacts and runs them as plain processes.

The image-only bet has an operational tail:

- **A registry becomes production infrastructure.** Kamal's deploy path is build → push to registry → every host pulls. That's a second credential, a monthly bill, and an availability dependency sitting in the middle of every deploy — including the 2am fix. hadi's path is build → SSH → flip; even image artifacts travel over SSH (`save | zstd | load`), so no registry exists to be down.
- **Docker on every host.** A root daemon to install, upgrade, and monitor, plus image storage to garbage-collect. hadi's boxes run systemd and Caddy; image-artifact services add daemonless podman, converged automatically.
- **Indirection in the ops path.** Containers under a daemon mean `docker exec` instead of `ps`, log drivers instead of journald, and kamal-proxy as one more moving part. hadi services — containerized or not — are children of systemd units: native logs, native cgroups, native everything.

When Kamal is the right call: your whole workflow is image-shaped end to end — you want the exact production blob running locally and in CI, image scanning in the pipeline, and accessories (Postgres, Redis) managed by the same tool. If you're all-in on the container ecosystem, Kamal embraces it; hadi deliberately keeps it optional.

## hadi vs Dokku

[Dokku](https://dokku.com) is a self-hosted Heroku: install the platform on a box, `git push`, buildpacks detect your stack, plugins provision databases. For a hobby project on one server it's a genuinely great experience.

The differences are about who owns the box:

- **Dokku is a platform installed on the server; hadi is a client that visits.** Dokku owns the nginx config, the app lifecycle, the plugin state. Upgrading Dokku is an event. With hadi there is nothing to upgrade on the box — the newest hadi binary on your laptop is the whole system.
- **Builds happen on the production box.** `git push dokku` compiles your app where it serves traffic — memory pressure, build toolchains, and secrets on prod. hadi builds where your CI or laptop is and ships the result.
- **Single-box by design.** Dokku's model is one server; scheduler plugins bolt on more. hadi is multi-box natively — DNS names the fleet, deploys roll box by box, `hadi ls` sees everything.
- **Plugins own your databases.** Convenient until the day the plugin's opinion and your backup strategy disagree. hadi draws the line deliberately: machines and databases belong to provisioning (terraform, cloud-init); hadi begins where a running box ends.

When Dokku is the right call: one box, Heroku muscle memory, buildpacks so you never write a Dockerfile, and one-liner addons — for a side project, that convenience is the whole point.

## hadi vs Kubernetes (k3s)

Kubernetes — including the lighter k3s — answers a real question: how do hundreds of workloads share a fleet with bin-packing, autoscaling, self-healing, and an operator ecosystem. If you have that question, nothing else answers it as well.

The mismatch is scale-shaped:

- **On a small fleet, orchestration is a solved non-problem.** systemd restarts crashed processes. Your load balancer health-checks boxes. Terraform scales counts. What k8s adds at this scale is the control plane itself: etcd, upgrades, CNI, ingress controllers, admission webhooks — a platform whose care exceeds the services it hosts.
- **The abstraction bill comes due while debugging.** A request traverses ingress → service → pod → container; a deploy traverses manifests → controllers → schedulers → kubelets. Every layer is a place to look. hadi's request path is Caddy → process, and its deploy is eleven readable steps over SSH.
- **k8s requires the container ecosystem** — registry, images, the works — as table stakes. hadi treats even containers as just another artifact kind.

When Kubernetes is the right call: dozens-plus of services with spiky loads worth bin-packing, a team that standardizes on its API, autoscaling you actually use, or operators running stateful systems for you. Those are real needs; a five-service fleet on five boxes just doesn't have them.

## The scorecard

| | hadi | Kamal | Dokku | k8s / k3s |
|---|---|---|---|---|
| Unit of deployment | binary / tarball / image | container image | git push / buildpack | container image |
| On your servers | systemd + Caddy (+ podman opt-in) | Docker + kamal-proxy | the Dokku platform | the cluster |
| Registry required | never | yes | no (builds on box) | yes |
| Credentials | SSH key | SSH key + registry | SSH key | kubeconfig (+ registry) |
| Multi-box | native (DNS discovery) | native | bolt-on | native |
| Zero-downtime deploys | blue-green + health gates | container swap + proxy | yes | rolling |
| Rollback | artifact already on box, seconds | re-pull image | rebuild | rollout undo |
| HTTPS | automatic (Caddy) | via kamal-proxy | via plugin | via ingress + cert-manager |
| Databases/accessories | your provisioning's job | managed as accessories | plugins | operators/helm |
| Runs between deploys | nothing | proxy + daemon | platform | control plane |

## The bets, restated

hadi is the right tool when your fleet is small enough to name, your services have homes, and you'd rather operate Linux than a platform. It is deliberately not trying to be the other three: no registry it could require, no daemon it could install, no plugin it could own your database with, no control plane it could ask you to upgrade. If your needs outgrow those bets — image-native workflows end to end, or genuine orchestration — Kamal and Kubernetes are good at exactly what they charge for.
