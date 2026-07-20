package main

// hadi rm: the inverse of ensure + deploy. Retires a service from its boxes:
// stop and disable both colors, remove the unit template, the caddy site,
// /opt/<name> and /etc/<name>; image services also lose their podman images.
//
// What it deliberately leaves: the run.user (provisioning creates users, hadi
// does not — symmetric with ensure's refusal to create them) and any
// extra_units files (their names aren't tracked on the box; they're inert
// once the service is gone).

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mitteai/hadi/internal/caddy"
	"github.com/mitteai/hadi/internal/config"
	"github.com/mitteai/hadi/internal/discover"
	"github.com/mitteai/hadi/internal/sshx"
	"github.com/mitteai/hadi/internal/ui"
	"golang.org/x/term"
)

func cmdRm(service, zone, hostFlag, sshKeyFlag string, dryRun, force bool) {
	// Never infer the target from the current repo: `hadi rm` in the wrong
	// terminal must not be able to take down the service you're standing in.
	if service == "" {
		ui.Usage("hadi rm requires an explicit -s <service> (it never infers one from ./deploy.json)")
	}
	// The argument becomes a path (/opt/<service>/hadi.json) before any config
	// is loaded — reject traversal/injection shapes at the door.
	if !config.ValidName(service) {
		ui.Usage("invalid service name %q (must match ^[a-z0-9][a-z0-9_-]{0,63}$)", service)
	}

	key, err := sshx.LoadKey(sshKeyFlag)
	if err != nil {
		ui.Fail("%v", err)
	}

	// Boxes: --host wins; otherwise DNS discovery. Resolved before dialing so
	// services without discovery records (previews, one-box services deployed
	// via hosts/--host) are removable with -s + --host.
	var boxes []string
	if hostFlag != "" {
		boxes = []string{hostFlag}
	} else {
		// Explicit sources only: the flag, then HADI_ZONE — never the local
		// deploy.json (a destructive command must not resolve discovery from
		// whatever repo the terminal happens to be in).
		if zone == "" {
			zone = os.Getenv("HADI_ZONE")
		}
		if zone == "" {
			ui.Usage("hadi rm needs --host <addr>, or an explicit zone (--zone / HADI_ZONE) for discovery")
		}
		if boxes, err = discover.Boxes(service, zone, nil); err != nil {
			ui.Fail("%v", err)
		}
	}

	ctx := &cmdCtx{key: key, conns: map[string]*sshx.Client{}, boxes: boxes}
	defer ctx.close()

	// The config comes from the first box's hadi.json — the box describes
	// what's on it (colors, entry, kind), which is exactly what rm must undo.
	cl, err := ctx.dial(boxes[0])
	if err != nil {
		ui.Fail("%v", err)
	}
	st, err := readState(cl, service)
	if err != nil {
		ui.Fail("%v", err)
	}
	if st == nil || st.Config == nil {
		ui.Fail("%s has no hadi.json on %s — nothing hadi-deployed to remove.\n(Half-provisioned leftovers, by hand: rm -rf /opt/%s /etc/%s /etc/caddy/hadi/%s.caddy /etc/systemd/system/%s@.service && systemctl daemon-reload && systemctl reload caddy)",
			service, boxes[0], service, service, service, service)
	}
	c := st.Config
	// The box's self-description decides what gets removed, so it must agree
	// with what the operator asked for: a corrupt or hand-edited hadi.json
	// naming a DIFFERENT service (or an empty name) must never redirect the
	// removal — /opt/pr-123/hadi.json claiming "mitte" would otherwise make
	// `hadi rm -s pr-123` delete mitte.
	if c.Name != service {
		ui.Fail("refusing: %s/hadi.json on %s names %q, not %q — the state is corrupt or hand-edited; inspect it before removing anything",
			"/opt/"+service, boxes[0], c.Name, service)
	}
	c.ApplyDefaults()

	ui.Say("removing %s from %d box(es): %s", c.Name, len(boxes), strings.Join(boxes, ", "))
	ui.Say("  units %s@{%d,%d} · /opt/%s · /etc/%s · %s%s",
		c.Name, c.Colors[0], c.Colors[1], c.Name, c.Name, caddy.SitePath(c.Name), imageNote(c))

	if dryRun {
		ui.Say("dry run: nothing touched")
		return
	}

	// Removal is permanent (artifacts, release ledger, env — all of it), so a
	// human types the service name; CI passes --force. There is no reliable
	// box-side signal that separates "prod service someone fat-fingered" from
	// "preview that must die", so the guard is confirmation, not detection.
	if !force {
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			ui.Fail("refusing to remove %s without --force in a non-interactive session", c.Name)
		}
		fmt.Printf("this permanently removes %s (artifacts, env, release history). type %q to confirm: ", c.Name, c.Name)
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if strings.TrimSpace(line) != c.Name {
			ui.Fail("confirmation mismatch; nothing touched")
		}
	}

	err = ctx.eachBox(func(cl *sshx.Client, first bool) error {
		return removeService(cl, c)
	})
	if err != nil {
		ui.Fail("%v", err)
	}
	ui.Say("\n%s removed (user %q and any extra_units left in place)", c.Name, c.Run.User)
}

func imageNote(c *config.Config) string {
	if c.IsImage() {
		return " · podman images " + c.BoxImage()
	}
	return ""
}

// removeService tears one service off one box. Every step tolerates absence
// (a half-deployed or half-removed box converges to gone), and the deploy
// lock is taken first so rm can't race an in-flight deploy.
func removeService(cl box, c *config.Config) error {
	// Last-line guard, independent of every caller: the name feeds
	// `rm -rf /opt/<name>` — an empty or metacharacter name would turn that
	// into `rm -rf /opt/` or shell injection. cmdRm validates earlier;
	// this holds for any future caller too.
	if !config.ValidName(c.Name) {
		return fmt.Errorf("refusing to remove service with unsafe name %q", c.Name)
	}
	if err := lock(cl, c.Name); err != nil {
		return err
	}
	// No unlock: /opt/<name> — the lock included — is removed below. If a step
	// fails midway the abandoned lock expires after 30 minutes, same as any
	// crashed deploy.

	t := time.Now()
	// Absence is tolerated per command (|| true), but a color still ACTIVE
	// after the stop attempt hard-fails: a broken systemd/dbus must not let
	// the dirs be removed under a running process (deleted-inode limbo, bound
	// ports, a ghost service).
	stop := fmt.Sprintf(
		"systemctl stop %[1]s@%[2]d %[1]s@%[3]d 2>/dev/null || true; systemctl disable %[1]s@%[2]d %[1]s@%[3]d 2>/dev/null || true; systemctl reset-failed '%[1]s@%[2]d' '%[1]s@%[3]d' 2>/dev/null || true; if systemctl is-active --quiet '%[1]s@%[2]d' 2>/dev/null || systemctl is-active --quiet '%[1]s@%[3]d' 2>/dev/null; then echo 'a color is still active after stop'; exit 1; fi",
		c.Name, c.Colors[0], c.Colors[1])
	if out, err := cl.Run(stop); err != nil {
		return fmt.Errorf("[%s] stop colors: %w\n%s", cl.Addr(), err, out)
	}
	ui.Step(cl.Addr(), "stop", fmt.Sprintf("%s@{%d,%d} stopped + disabled", c.Name, c.Colors[0], c.Colors[1]), time.Since(t), true)

	t = time.Now()
	units := fmt.Sprintf("rm -f /etc/systemd/system/%s && systemctl daemon-reload", c.TemplateUnitName())
	if out, err := cl.Run(units); err != nil {
		return fmt.Errorf("[%s] remove unit: %w\n%s", cl.Addr(), err, out)
	}
	ui.Step(cl.Addr(), "units", c.TemplateUnitName()+" removed, daemon-reload", time.Since(t), true)

	t = time.Now()
	site := fmt.Sprintf("rm -f %s && (systemctl reload caddy 2>/dev/null || systemctl restart caddy 2>/dev/null || true)", caddy.SitePath(c.Name))
	if out, err := cl.Run(site); err != nil {
		return fmt.Errorf("[%s] remove caddy site: %w\n%s", cl.Addr(), err, out)
	}
	ui.Step(cl.Addr(), "caddy", caddy.SitePath(c.Name)+" removed, reloaded", time.Since(t), true)

	if c.IsImage() {
		t = time.Now()
		images := fmt.Sprintf("podman images -q %s 2>/dev/null | sort -u | xargs -r podman rmi -f >/dev/null 2>&1; true", c.BoxImage())
		if out, err := cl.Run(images); err != nil {
			return fmt.Errorf("[%s] remove images: %w\n%s", cl.Addr(), err, out)
		}
		ui.Step(cl.Addr(), "images", c.BoxImage()+" removed", time.Since(t), true)
	}

	t = time.Now()
	dirs := fmt.Sprintf("rm -rf /opt/%[1]s /etc/%[1]s", c.Name)
	if out, err := cl.Run(dirs); err != nil {
		return fmt.Errorf("[%s] remove dirs: %w\n%s", cl.Addr(), err, out)
	}
	ui.Step(cl.Addr(), "dirs", fmt.Sprintf("/opt/%[1]s + /etc/%[1]s removed", c.Name), time.Since(t), true)
	return nil
}
