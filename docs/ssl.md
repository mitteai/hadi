# SSL / TLS

The short version:

- Public services (`"entry": {"domain": ...}`) get HTTPS automatically. No configuration, no flags, no certificate files to manage.
- Certificates come from Let's Encrypt, obtained by Caddy on the box the first time the domain is hit.
- **Renewal is automatic, forever.** Caddy runs as a daemon and renews each certificate at about two-thirds of its lifetime (roughly 30 days before expiry), retrying with backoff if the CA is unreachable. There is no cron job to install, no command to remember, and nothing that breaks if nobody deploys for six months.
- hadi itself contains zero certificate code. It writes Caddy's config once; Caddy owns the entire certificate lifecycle after that.
- Internal services (`"entry": {"port": ...}`) carry no TLS on the box. Your load balancer terminates HTTPS in front of them, with whatever certificate mechanism it uses.

## What you need for it to work

1. `"entry": {"domain": "api.example.com"}` in deploy.json.
2. A public A record: `api.example.com` pointing at the box (DNS-only; a CDN proxy in front changes the picture, see below).
3. Ports 80 and 443 reachable on the box. Let's Encrypt validates over them (HTTP-01 / TLS-ALPN-01), and Caddy answers the challenges itself.

Deploy. The first HTTPS request triggers issuance (a few seconds); everything after is ordinary traffic.

## What's on the box

`hadi ensure` installs Caddy as a systemd service and writes a site config per service under `/etc/caddy/hadi/`. A domain entry renders to a site block for your domain, which is Caddy's signal to manage its certificate. Certificates and account keys live in Caddy's own storage (`/var/lib/caddy`); treat them as Caddy's business.

Because renewal belongs to a daemon on the box, it's independent of hadi, of your CI, and of anyone remembering anything. A box that hasn't been deployed to in months still renews on schedule.

## Checking on it

```bash
# expiry date, from the outside
echo | openssl s_client -connect api.example.com:443 2>/dev/null | openssl x509 -noout -enddate

# Caddy's own logs, on the box
hadi exec -s api 'journalctl -u caddy -n 20 --no-pager'
```

If issuance fails (DNS not pointing at the box yet, port 80 blocked), Caddy retries on its own; the journal says exactly what Let's Encrypt objected to.

## Behind a CDN or proxy

If Cloudflare (or similar) proxies the domain, the CDN terminates public TLS with its own certificate and Let's Encrypt's challenges may never reach your box. Either keep the record DNS-only so Caddy manages the certificate end to end, or lean fully on the CDN's certificates and its origin-encryption story. Both work; pick one deliberately rather than layering them by accident.

## Load-balanced services

For port entries, the certificate belongs to whatever terminates TLS, usually your load balancer, and is provisioned wherever that is (terraform, typically). One warning from experience: if your LB certificate comes from an infrastructure-as-code ACME provider, renewal often happens **only when someone runs an apply**. That's renew-on-apply, not auto-renewal; make sure something applies regularly, or an alert watches expiry. Caddy's on-box renewal has no such failure mode, which is a real argument for domain entries where a load balancer isn't otherwise needed.
