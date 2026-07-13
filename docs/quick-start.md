# Quick start: hello world to production

Deploy a tiny Go server to a real box, with HTTPS, in about five minutes. You'll need one Linux box (Debian-family, root SSH) and a domain you control.

## 1. The service

Two rules make any service hadi-deployable: read your port from an env var, and answer a health path.

```go
// main.go
package main

import (
	"fmt"
	"net/http"
	"os"
)

func main() {
	greeting := os.Getenv("GREETING")
	if greeting == "" {
		greeting = "hello world"
	}
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, greeting)
	})
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	http.ListenAndServe(":"+os.Getenv("PORT"), nil)
}
```

```bash
go mod init hello
```

## 2. The config

```json
{
  "name": "hello",
  "zone": "example.com",
  "hosts": ["203.0.113.10"],
  "build": "CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/hello .",
  "artifact": "bin/hello",
  "run": {"port_env": "PORT"},
  "entry": {"domain": "hello.example.com"}
}
```

That's the whole deployment surface. `hosts` points straight at your box (swap in DNS discovery later); `entry.domain` means public HTTPS with automatic certificates.

## 3. The box

Two things hadi won't do for you, because machines are your provisioning's job:

```bash
# an A record: hello.example.com → 203.0.113.10  (at your DNS host, DNS-only)

# the service user, on the box:
ssh root@203.0.113.10 'useradd --system --create-home --home /opt/hello --shell /usr/sbin/nologin hello'
```

## 4. Check, then deploy

```bash
$ hadi check
service   hello (zone example.com)
entry     https://hello.example.com (caddy terminates TLS, auto-renewed)
colors    4001 / 4002   health /healthz   ready_timeout 60s   stop_timeout 90s
boxes     203.0.113.10 (static hosts)
...
ok

$ hadi deploy
hello 3f2c91a → 1 box(es) (203.0.113.10)

[203.0.113.10] ensure   caddy + dirs + site (idempotent)               14.2s
[203.0.113.10] ship     artifact 3f2c91a (6.1MB)                        0.9s
[203.0.113.10] units    hello@.service + 0 extra, daemon-reload         0.4s
[203.0.113.10] start    hello@4001 (idle color)                         0.1s
[203.0.113.10] verify   /healthz on :4001  ok                           0.1s
[203.0.113.10] flip     caddy → :4001                                   0.2s
[203.0.113.10] confirm  /healthz through front door  ok                 0.3s
[203.0.113.10] drain    hello@4002 (≤90s, non-blocking)

deployed 3f2c91a in 16.3s · rollback: hadi rollback
```

The first `ensure` installs Caddy, so it's slow once; every deploy after runs in a few seconds.

```bash
$ curl https://hello.example.com
hello world
```

Live, with a certificate you never think about again.

## 5. Ship a change, watch it flip

Edit the greeting in main.go, commit, and:

```bash
$ hadi deploy
...
[203.0.113.10] start    hello@4002 (idle color)
[203.0.113.10] flip     caddy → :4002
```

Note the ports: the new version came up on 4002 while 4001 kept serving, and traffic moved only after 4002 proved healthy. Run `while true; do curl -s https://hello.example.com; sleep 0.2; done` in another terminal during the deploy; you won't catch a single error.

## 6. Play with the rest

```bash
hadi status                      # live color, sha, health
hadi env set GREETING="hi from hadi"   # config change, zero downtime
curl https://hello.example.com   # → hi from hadi
hadi logs -f                     # follow the journal
hadi rollback                    # previous release, same safety as a deploy
```

Every one of these is health-gated the same way the deploy was: nothing replaces a working version without proving itself first.

## Where next

- [config.md](config.md): every deploy.json option (hardening, hooks, release tarballs, sidecars)
- [ci.md](ci.md): auto-deploy on push with one secret
- [dns.md](dns.md): swap `hosts` for DNS discovery so scaling needs no config edits
- [troubleshooting.md](troubleshooting.md): when something doesn't go like this page
