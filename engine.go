package main

// The lifecycle engine: everything hadi does to one box. deploy, env, and
// rollback are all thin wrappers around ensureBox + placeArtifact + flip.
// The opinion lives here, once.

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mitteai/hadi/internal/caddy"
	"github.com/mitteai/hadi/internal/config"
	"github.com/mitteai/hadi/internal/ui"
	"github.com/mitteai/hadi/internal/unit"
)

// box is what the engine needs from a machine: run a command, push a file
// (or stream one), say who you are. sshx.Client satisfies it; tests use a
// fake. The whole lifecycle is testable without a single real box.
type box interface {
	Run(cmd string) (string, error)
	Push(content []byte, path, mode string) error
	PushReader(r io.Reader, path, mode string) error
	Addr() string
}

// boxState is /opt/<name>/hadi.json — the box describing itself.
type boxState struct {
	Name       string         `json:"name"`
	Active     int            `json:"active"`
	SHA        string         `json:"sha"`
	PrevSHA    string         `json:"prev_sha,omitempty"`
	DeployedAt string         `json:"deployed_at"`
	Deployer   string         `json:"deployer"`
	Config     *config.Config `json:"config"`
}

func statePath(name string) string { return "/opt/" + name + "/hadi.json" }
func lockPath(name string) string  { return "/opt/" + name + "/hadi.lock" }

// readState loads the box's hadi.json, or nil if absent.
func readState(cl box, name string) (*boxState, error) {
	out, err := cl.Run("cat " + statePath(name) + " 2>/dev/null || true")
	if err != nil || strings.TrimSpace(out) == "" {
		return nil, err
	}
	var st boxState
	if jerr := json.Unmarshal([]byte(out), &st); jerr != nil {
		return nil, fmt.Errorf("[%s] corrupt %s: %v", cl.Addr(), statePath(name), jerr)
	}
	return &st, nil
}

func writeState(cl box, st *boxState) error {
	raw, _ := json.MarshalIndent(st, "", "  ")
	return cl.Push(raw, statePath(st.Name), "0644")
}

// lock takes the per-box deploy lock; fails fast if someone else holds it.
// Locks older than 30 minutes are considered abandoned.
func lock(cl box, name string) error {
	cmd := fmt.Sprintf(
		`if [ -f %[1]s ] && [ $(( $(date +%%s) - $(stat -c %%Y %[1]s) )) -lt 1800 ]; then echo "HELD: $(cat %[1]s)"; exit 1; fi; mkdir -p /opt/%[2]s && echo "$(date -u +%%FT%%TZ) $$" > %[1]s`,
		lockPath(name), name)
	out, err := cl.Run(cmd)
	if err != nil {
		return fmt.Errorf("[%s] another hadi holds the deploy lock (%s). If it's dead, remove %s", cl.Addr(), strings.TrimPrefix(out, "HELD: "), lockPath(name))
	}
	return nil
}

func unlock(cl box, name string) { _, _ = cl.Run("rm -f " + lockPath(name)) }

// activeColor determines which color is live. The caddy config outranks
// hadi.json: traffic goes where caddy points, and anything else (a bash
// deploy, a hand edit) may have flipped caddy without updating state.
// hadi.json is metadata; caddy is the truth.
func activeColor(cl box, c *config.Config) (int, error) {
	out, _ := cl.Run("cat /etc/caddy/hadi/" + c.Name + ".caddy 2>/dev/null || cat /etc/caddy/Caddyfile 2>/dev/null || true")
	if strings.TrimSpace(out) != "" {
		if port, err := caddy.ActiveColor(out, c); err == nil {
			return port, nil
		}
	}
	if st, _ := readState(cl, c.Name); st != nil && st.Active != 0 {
		return st.Active, nil
	}
	// Fresh box: first color is the convention.
	return c.Colors[0], nil
}

// ensureBox converges hadi's own layer: caddy, site config, dirs, current
// symlink. Idempotent; a no-op after the first run.
func ensureBox(cl box, c *config.Config) error {
	t := time.Now()

	// Detect the live upstream BEFORE touching any caddy config, so
	// migrating an existing box preserves its active color.
	active, err := activeColor(cl, c)
	if err != nil {
		return err
	}

	// Image services skip the bin/releases dirs and the current symlink
	// (vestigial for containers) and converge podman + zstd instead. /opt/<name>
	// itself stays: hadi.json, the lock, and releases.log live there.
	dirs := fmt.Sprintf(`mkdir -p /opt/%[1]s/bin /opt/%[1]s/releases /etc/%[1]s /etc/caddy/hadi
[ -e /opt/%[1]s/current ] || ln -s /opt/%[1]s /opt/%[1]s/current`, c.Name)
	runtime := ""
	if c.IsImage() {
		dirs = fmt.Sprintf("mkdir -p /opt/%[1]s /etc/%[1]s /etc/caddy/hadi", c.Name)
		runtime = `
command -v podman >/dev/null || { apt-get update -y >/dev/null 2>&1; apt-get install -y podman >/dev/null 2>&1; }
command -v zstd >/dev/null || apt-get install -y zstd >/dev/null 2>&1`
	}
	script := fmt.Sprintf(`set -e
id -u %[1]s >/dev/null 2>&1 || { echo "user %[1]s missing: provisioning (terraform) creates users, hadi does not"; exit 1; }
%[3]s
touch /etc/%[2]s/env && chown %[1]s /etc/%[2]s/env && chmod 0640 /etc/%[2]s/env%[4]s
if ! command -v caddy >/dev/null; then
  apt-get install -y debian-keyring debian-archive-keyring apt-transport-https >/dev/null 2>&1
  curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | gpg --dearmor --yes -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
  curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' > /etc/apt/sources.list.d/caddy-stable.list
  apt-get update -y >/dev/null 2>&1 && apt-get install -y caddy >/dev/null 2>&1
fi`, c.Run.User, c.Name, dirs, runtime)
	if out, err := cl.Run(script); err != nil {
		return fmt.Errorf("ensure: %w\n%s", err, out)
	}

	// Main Caddyfile is hadi-owned: converge it whenever it differs from the
	// blessed content (first migration, or a template update like the
	// trusted_proxies addition), not only when the import line is missing.
	main, _ := cl.Run("cat /etc/caddy/Caddyfile 2>/dev/null || true")
	if strings.TrimSpace(main) != strings.TrimSpace(caddy.MainCaddyfile) {
		if err := cl.Push([]byte(caddy.MainCaddyfile), "/etc/caddy/Caddyfile", "0644"); err != nil {
			return err
		}
	}
	// Site config, preserving the detected active color.
	if err := cl.Push([]byte(caddy.RenderSite(c, active)), caddy.SitePath(c.Name), "0644"); err != nil {
		return err
	}
	if out, err := cl.Run("systemctl enable caddy >/dev/null 2>&1; systemctl reload caddy 2>/dev/null || systemctl restart caddy"); err != nil {
		return fmt.Errorf("caddy reload: %w\n%s", err, out)
	}
	ui.Step(cl.Addr(), "ensure", "caddy + dirs + site (idempotent)", time.Since(t), true)
	return nil
}

// installUnits renders the template unit and ships extra units, then reloads.
func installUnits(cl box, c *config.Config, extraUnits map[string][]byte) error {
	t := time.Now()
	var unitText string
	if c.Run.UnitFile != "" {
		raw, ok := extraUnits["__unit_file__"]
		if !ok {
			return fmt.Errorf("run.unit_file %s not readable", c.Run.UnitFile)
		}
		unitText = string(raw)
	} else {
		unitText = unit.Render(c)
	}
	// Image units carry uid/gid tokens: the render is pure local string
	// building, and only the box knows what uid run.user maps to (%U in a
	// system unit is the manager's user — root — never User=).
	if strings.Contains(unitText, unit.UIDToken) {
		uid, err := cl.Run("id -u " + c.Run.User)
		if err != nil {
			return fmt.Errorf("resolve uid of %s: %w", c.Run.User, err)
		}
		gid, err := cl.Run("id -g " + c.Run.User)
		if err != nil {
			return fmt.Errorf("resolve gid of %s: %w", c.Run.User, err)
		}
		unitText = strings.ReplaceAll(unitText, unit.UIDToken, strings.TrimSpace(uid))
		unitText = strings.ReplaceAll(unitText, unit.GIDToken, strings.TrimSpace(gid))
	}
	if err := cl.Push([]byte(unitText), "/etc/systemd/system/"+c.Name+"@.service", "0644"); err != nil {
		return err
	}
	n := 0
	for fname, content := range extraUnits {
		if fname == "__unit_file__" {
			continue
		}
		if err := cl.Push(content, "/etc/systemd/system/"+fname, "0644"); err != nil {
			return err
		}
		n++
	}
	if _, err := cl.Run("systemctl daemon-reload"); err != nil {
		return err
	}
	ui.Step(cl.Addr(), "units", fmt.Sprintf("%s@.service + %d extra, daemon-reload", c.Name, n), time.Since(t), true)
	return nil
}

// guardEnv refuses to proceed if the env file pins the port variable, which
// would silently override the unit's per-color port and break the flip. For
// image services it additionally refuses quoted values: systemd's
// EnvironmentFile strips quotes, podman's --env-file takes lines literally —
// FOO="a b" would silently change value on the kind switch.
func guardEnv(cl box, c *config.Config) error {
	out, _ := cl.Run(fmt.Sprintf("grep -c '^%s=' /etc/%s/env 2>/dev/null || true", c.Run.PortEnv, c.Name))
	if strings.TrimSpace(out) != "0" && strings.TrimSpace(out) != "" {
		return fmt.Errorf("[%s] /etc/%s/env sets %s, which would override the unit's per-color port and break blue-green. Remove that line (hadi env edit -s %s)",
			cl.Addr(), c.Name, c.Run.PortEnv, c.Name)
	}
	if c.IsImage() {
		quoted, _ := cl.Run(fmt.Sprintf(`grep -nE "^[A-Za-z_][A-Za-z0-9_]*=[\"']" /etc/%s/env 2>/dev/null || true`, c.Name))
		if strings.TrimSpace(quoted) != "" {
			return fmt.Errorf("[%s] /etc/%s/env has quoted values; podman --env-file takes lines literally (the quotes would become part of the value). Unquote them (hadi env edit -s %s):\n%s",
				cl.Addr(), c.Name, c.Name, quoted)
		}
	}
	return nil
}

// healthCmd builds the box-side curl for a color or the front door.
func healthCmd(c *config.Config, port int) string {
	return fmt.Sprintf("curl -sf --max-time 5 http://127.0.0.1:%d%s", port, c.Health)
}

func frontCmd(c *config.Config) string {
	if c.Entry.Domain != "" {
		return fmt.Sprintf("curl -skf --max-time 10 --resolve %s:443:127.0.0.1 https://%s%s", c.Entry.Domain, c.Entry.Domain, c.Health)
	}
	return fmt.Sprintf("curl -sf --max-time 5 http://127.0.0.1:%d%s", c.Entry.Port, c.Health)
}

// evidence gathers the failing color's journal tail and last health response.
func evidence(cl box, c *config.Config, color int) string {
	health, _ := cl.Run(fmt.Sprintf("curl -s --max-time 5 -w ' (%%{http_code})' http://127.0.0.1:%d%s || true", color, c.Health))
	journal, _ := cl.Run(fmt.Sprintf("journalctl -u %s@%d -n 5 --no-pager -o short 2>/dev/null | tail -5", c.Name, color))
	return fmt.Sprintf("last health response: %s\njournal %s@%d (last 5 lines):\n%s", strings.TrimSpace(health), c.Name, color, journal)
}

// flip performs: start idle color, verify, [onceHook], flip caddy, confirm
// front door, retire old color, record state. The heart of hadi.
func flip(cl box, c *config.Config, sha, deployer string, runOnceHook bool) error {
	active, err := activeColor(cl, c)
	if err != nil {
		return err
	}
	next := c.OtherColor(active)

	if err := guardEnv(cl, c); err != nil {
		return err
	}

	// Start the idle color; the live one serves untouched through all of this.
	t := time.Now()
	if out, err := cl.Run(fmt.Sprintf("systemctl reset-failed '%s@%d' 2>/dev/null; systemctl restart %s@%d", c.Name, next, c.Name, next)); err != nil {
		return fmt.Errorf("start %s@%d: %w\n%s", c.Name, next, err, out)
	}
	ui.Step(cl.Addr(), "start", fmt.Sprintf("%s@%d (idle color)", c.Name, next), time.Since(t), true)

	// Verify the new color directly.
	t = time.Now()
	deadline := time.Now().Add(time.Duration(c.Run.ReadyTimeout) * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		if _, err := cl.Run(healthCmd(c, next)); err == nil {
			ready = true
			break
		}
		time.Sleep(2 * time.Second)
	}
	if !ready {
		ui.Step(cl.Addr(), "verify", fmt.Sprintf("%s on :%d", c.Health, next), time.Since(t), false)
		ui.Detail(evidence(cl, c, next))
		_, _ = cl.Run(fmt.Sprintf("systemctl stop %s@%d", c.Name, next))
		ui.Detail(fmt.Sprintf("cleaned up: @%d stopped · @%d never stopped serving", next, active))
		return fmt.Errorf("new color never went ready after %ds", c.Run.ReadyTimeout)
	}
	ui.Step(cl.Addr(), "verify", fmt.Sprintf("%s on :%d", c.Health, next)+"  ok", time.Since(t), true)

	// Once-per-deploy hook (migrations): after verification, before traffic.
	// Image kind runs it where the app lives — a one-shot container of the new
	// sha, through /bin/sh so the hook string keeps shell semantics (&&, $VARS
	// from the env file). It cannot touch box paths; that is the documented
	// kind difference. Plain kinds run it box-side as always.
	if runOnceHook && c.Hooks.OnceBeforeFlip != "" {
		t = time.Now()
		hookCmd := fmt.Sprintf("cd /opt/%s/current && sudo -u %s %s", c.Name, c.Run.User, c.Hooks.OnceBeforeFlip)
		if c.IsImage() {
			hookCmd = fmt.Sprintf("podman run --rm --pull=never --network host --user $(id -u %[1]s):$(id -g %[1]s) --env-file /etc/%[2]s/env --entrypoint /bin/sh %[3]s:%[4]s -c %[5]q",
				c.Run.User, c.Name, c.BoxImage(), sha, c.Hooks.OnceBeforeFlip)
		}
		if out, err := cl.Run(hookCmd); err != nil {
			_, _ = cl.Run(fmt.Sprintf("systemctl stop %s@%d", c.Name, next))
			return fmt.Errorf("once_before_flip failed (old color still serving): %w\n%s", err, out)
		}
		ui.Step(cl.Addr(), "hook", "once_before_flip: "+truncate(c.Hooks.OnceBeforeFlip, 40), time.Since(t), true)
	}

	// Flip Caddy.
	t = time.Now()
	if err := cl.Push([]byte(caddy.RenderSite(c, next)), caddy.SitePath(c.Name), "0644"); err != nil {
		return err
	}
	if out, err := cl.Run("systemctl reload caddy"); err != nil {
		return fmt.Errorf("caddy reload: %w\n%s", err, out)
	}
	ui.Step(cl.Addr(), "flip", fmt.Sprintf("caddy → :%d", next), time.Since(t), true)

	// Confirm through the front door: only a request through Caddy's own
	// listener proves the flip. Failure here flips back.
	t = time.Now()
	if _, err := cl.Run(frontCmd(c)); err != nil {
		ui.Step(cl.Addr(), "confirm", "front door", time.Since(t), false)
		_ = cl.Push([]byte(caddy.RenderSite(c, active)), caddy.SitePath(c.Name), "0644")
		_, _ = cl.Run("systemctl reload caddy")
		_, _ = cl.Run(fmt.Sprintf("systemctl stop %s@%d", c.Name, next))
		return fmt.Errorf("front-door check failed after flip; flipped back, old color serving")
	}
	ui.Step(cl.Addr(), "confirm", fmt.Sprintf("%s through front door  ok", c.Health), time.Since(t), true)

	// Retire the old color: enable new for boot, drain old without blocking.
	_, _ = cl.Run(fmt.Sprintf("systemctl enable %s@%d >/dev/null 2>&1; systemctl disable %s@%d >/dev/null 2>&1; systemctl stop --no-block %s@%d",
		c.Name, next, c.Name, active, c.Name, active))
	ui.Step(cl.Addr(), "drain", fmt.Sprintf("%s@%d (≤%ds, non-blocking)", c.Name, active, c.Run.StopTimeout), 0, true)

	if c.Hooks.AfterFlip != "" {
		if out, err := cl.Run(c.Hooks.AfterFlip); err != nil {
			ui.Say("warning: after_flip hook failed (deploy already live): %v\n%s", err, out)
		}
	}

	// Record.
	prev, _ := readState(cl, c.Name)
	st := &boxState{Name: c.Name, Active: next, SHA: sha, DeployedAt: time.Now().UTC().Format(time.RFC3339), Deployer: deployer, Config: c}
	if prev != nil {
		st.PrevSHA = prev.SHA
	}
	if err := writeState(cl, st); err != nil {
		return err
	}
	// 5th column (artifact kind) landed with image support; readers tolerate
	// legacy 4-column lines, and rollback uses it to refuse crossing the
	// image boundary.
	_, _ = cl.Run(fmt.Sprintf("printf '%%s\\t%%s\\t%%d\\t%%s\\t%%s\\n' '%s' '%s' %d '%s' '%s' >> /opt/%s/releases.log",
		st.DeployedAt, sha, next, deployer, c.Kind(), c.Name))
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
