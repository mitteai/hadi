package main

// The engine tested against a fake box: the scariest code path becomes the
// best-tested one, exactly as the proposal promised. No real SSH anywhere.

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/mitteai/hadi/internal/config"
)

// fakeBox scripts responses by command substring and records everything.
type fakeBox struct {
	addr   string
	rules  []rule // first substring match wins
	ran    []string
	pushes map[string]string // path → content
}

type rule struct {
	match string
	out   string
	err   error
}

func newFakeBox(rules ...rule) *fakeBox {
	return &fakeBox{addr: "box-under-test", rules: rules, pushes: map[string]string{}}
}

func (f *fakeBox) Run(cmd string) (string, error) {
	f.ran = append(f.ran, cmd)
	for _, r := range f.rules {
		if strings.Contains(cmd, r.match) {
			return r.out, r.err
		}
	}
	return "", nil
}

func (f *fakeBox) Push(content []byte, path, mode string) error {
	f.pushes[path] = string(content)
	return nil
}

func (f *fakeBox) PushReader(r io.Reader, path, mode string) error {
	raw, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	return f.Push(raw, path, mode)
}

func (f *fakeBox) Addr() string { return f.addr }

func (f *fakeBox) didRun(sub string) bool {
	for _, c := range f.ran {
		if strings.Contains(c, sub) {
			return true
		}
	}
	return false
}

func testCfg() *config.Config {
	c := &config.Config{
		Name:  "svc",
		Zone:  "example.com",
		Run:   config.Run{PortEnv: "PORT", ReadyTimeout: 1},
		Entry: config.Entry{Port: 4002},
	}
	c.ApplyDefaults()
	c.Run.ReadyTimeout = 1 // ApplyDefaults leaves nonzero values alone; keep polls short
	return c
}

func stateJSON(active int, sha string) string {
	st := boxState{Name: "svc", Active: active, SHA: sha}
	raw, _ := json.Marshal(st)
	return string(raw)
}

func TestFlipHappyPath(t *testing.T) {
	c := testCfg()
	f := newFakeBox(
		rule{match: "cat /opt/svc/hadi.json", out: stateJSON(4003, "old111")},
		rule{match: "grep -c '^PORT='", out: "0"},
		// health and front curls succeed by default ("" / nil)
	)
	if err := flip(f, c, "new222", "tester", false); err != nil {
		t.Fatalf("flip: %v", err)
	}
	if !f.didRun("systemctl restart svc@4004") {
		t.Error("idle color 4004 not started")
	}
	site := f.pushes["/etc/caddy/hadi/svc.caddy"]
	if !strings.Contains(site, "127.0.0.1:4004") {
		t.Errorf("caddy not flipped to 4004:\n%s", site)
	}
	if !f.didRun("systemctl stop --no-block svc@4003") {
		t.Error("old color not drained")
	}
	if !f.didRun("curl -sf --max-time 5 http://127.0.0.1:4002/healthz") {
		t.Error("front door never confirmed")
	}
	var st boxState
	if err := json.Unmarshal([]byte(f.pushes["/opt/svc/hadi.json"]), &st); err != nil || st.Active != 4004 || st.SHA != "new222" || st.PrevSHA != "old111" {
		t.Errorf("state not recorded correctly: %+v %v", st, err)
	}
	if !f.didRun("releases.log") {
		t.Error("release ledger not appended")
	}
}

func TestFlipVerifyFailureLeavesOldServing(t *testing.T) {
	c := testCfg()
	f := newFakeBox(
		rule{match: "cat /opt/svc/hadi.json", out: stateJSON(4003, "old111")},
		rule{match: "grep -c '^PORT='", out: "0"},
		rule{match: "http://127.0.0.1:4004/healthz", err: fmt.Errorf("connection refused")},
	)
	err := flip(f, c, "new222", "tester", false)
	if err == nil {
		t.Fatal("want failure when new color never goes ready")
	}
	if !f.didRun("systemctl stop svc@4004") {
		t.Error("failed color not cleaned up")
	}
	if site, ok := f.pushes["/etc/caddy/hadi/svc.caddy"]; ok && strings.Contains(site, "4004") {
		t.Error("caddy flipped despite failed verification")
	}
	if f.didRun("systemctl stop --no-block svc@4003") {
		t.Error("old color was touched; it must keep serving")
	}
}

func TestFlipFrontDoorFailureFlipsBack(t *testing.T) {
	c := testCfg()
	f := newFakeBox(
		rule{match: "cat /opt/svc/hadi.json", out: stateJSON(4003, "old111")},
		rule{match: "grep -c '^PORT='", out: "0"},
		rule{match: "http://127.0.0.1:4002/healthz", err: fmt.Errorf("502")}, // front fails
	)
	err := flip(f, c, "new222", "tester", false)
	if err == nil || !strings.Contains(err.Error(), "front-door") {
		t.Fatalf("want front-door failure, got %v", err)
	}
	site := f.pushes["/etc/caddy/hadi/svc.caddy"]
	if !strings.Contains(site, "127.0.0.1:4003") {
		t.Errorf("caddy not flipped back to old color:\n%s", site)
	}
	if !f.didRun("systemctl stop svc@4004") {
		t.Error("new color not stopped after flip-back")
	}
}

func TestFlipGuardsPortInEnv(t *testing.T) {
	c := testCfg()
	f := newFakeBox(
		rule{match: "cat /opt/svc/hadi.json", out: stateJSON(4003, "old111")},
		rule{match: "grep -c '^PORT='", out: "1"}, // env pins the port
	)
	err := flip(f, c, "new222", "tester", false)
	if err == nil || !strings.Contains(err.Error(), "PORT") {
		t.Fatalf("want env-guard refusal, got %v", err)
	}
	if f.didRun("systemctl restart svc@4004") {
		t.Error("must refuse before touching any color")
	}
}

func TestOnceHookFailureAbortsBeforeFlip(t *testing.T) {
	c := testCfg()
	c.Hooks.OnceBeforeFlip = "bin/migrate"
	f := newFakeBox(
		rule{match: "cat /opt/svc/hadi.json", out: stateJSON(4003, "old111")},
		rule{match: "grep -c '^PORT='", out: "0"},
		rule{match: "bin/migrate", err: fmt.Errorf("migration exploded")},
	)
	err := flip(f, c, "new222", "tester", true)
	if err == nil || !strings.Contains(err.Error(), "once_before_flip") {
		t.Fatalf("want hook failure, got %v", err)
	}
	if site, ok := f.pushes["/etc/caddy/hadi/svc.caddy"]; ok && strings.Contains(site, "4004") {
		t.Error("caddy flipped despite failed migration")
	}
}

func TestActiveColorPrecedence(t *testing.T) {
	c := testCfg()

	// 1. Caddy is the truth, even when hadi.json disagrees (a bash deploy
	// flips caddy without updating state).
	f := newFakeBox(
		rule{match: "cat /etc/caddy", out: ":4002 {\n reverse_proxy 127.0.0.1:4004\n}"},
		rule{match: "hadi.json", out: stateJSON(4003, "stale")},
	)
	if got, _ := activeColor(f, c); got != 4004 {
		t.Errorf("caddy must outrank state: got %d", got)
	}
	// 2. No caddy config: hadi.json fills in.
	f = newFakeBox(rule{match: "hadi.json", out: stateJSON(4004, "x")})
	if got, _ := activeColor(f, c); got != 4004 {
		t.Errorf("state fallback: got %d", got)
	}
	// 3. Fresh box: first color.
	f = newFakeBox()
	if got, _ := activeColor(f, c); got != c.Colors[0] {
		t.Errorf("fresh box default: got %d", got)
	}
}

func TestLockHeld(t *testing.T) {
	f := newFakeBox(rule{match: "hadi.lock", out: "HELD: 2026-07-12 999", err: fmt.Errorf("exit 1")})
	err := lock(f, "svc")
	if err == nil || !strings.Contains(err.Error(), "lock") {
		t.Fatalf("want lock error, got %v", err)
	}
}

func TestEnsurePreservesActiveColorOnMigratedBox(t *testing.T) {
	c := testCfg()
	// A pre-hadi box: raw Caddyfile pointing at the SECOND color. The site
	// read + Caddyfile fallback is one compound `cat a || cat b` command, so
	// the fake scripts it as one response (what a real shell would emit).
	f := newFakeBox(
		rule{match: "cat /etc/caddy/hadi/svc.caddy", out: ":4002 {\n reverse_proxy 127.0.0.1:4004\n}"},
	)
	if err := ensureBox(f, c); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	site := f.pushes["/etc/caddy/hadi/svc.caddy"]
	if !strings.Contains(site, "127.0.0.1:4004") {
		t.Errorf("migration clobbered the live color:\n%s", site)
	}
}

func imageCfg() *config.Config {
	c := &config.Config{
		Name:     "svc",
		Zone:     "example.com",
		Artifact: "image:svc:release",
		Run:      config.Run{PortEnv: "PORT", ReadyTimeout: 1},
		Entry:    config.Entry{Port: 4002},
	}
	c.ApplyDefaults()
	c.Run.ReadyTimeout = 1
	return c
}

func TestEnsureImageConvergesPodmanSkipsVestigialDirs(t *testing.T) {
	f := newFakeBox()
	if err := ensureBox(f, imageCfg()); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if !f.didRun("command -v podman") || !f.didRun("command -v zstd") {
		t.Error("image ensure must converge podman + zstd")
	}
	if f.didRun("/opt/svc/bin") || f.didRun("/opt/svc/releases") || f.didRun("ln -s /opt/svc /opt/svc/current") {
		t.Error("image ensure must not create binary/release dirs or the current symlink")
	}
	// The parts every kind needs stay.
	if !f.didRun("touch /etc/svc/env") {
		t.Error("env file convergence missing")
	}
}

func TestEnsurePlainKindUntouchedByImageSupport(t *testing.T) {
	f := newFakeBox()
	if err := ensureBox(f, testCfg()); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if !f.didRun("mkdir -p /opt/svc/bin /opt/svc/releases") || f.didRun("command -v podman") {
		t.Error("plain-kind ensure changed behavior")
	}
}

func TestInstallUnitsResolvesUIDTokensBoxSide(t *testing.T) {
	c := imageCfg()
	f := newFakeBox(
		rule{match: "id -u svc", out: "999"},
		rule{match: "id -g svc", out: "988"},
	)
	if err := installUnits(f, c, nil); err != nil {
		t.Fatalf("installUnits: %v", err)
	}
	u := f.pushes["/etc/systemd/system/svc@.service"]
	if !strings.Contains(u, "--user 999:988") {
		t.Errorf("uid/gid tokens not substituted:\n%s", u)
	}
	if strings.Contains(u, "{{UID}}") || strings.Contains(u, "{{GID}}") {
		t.Error("tokens leaked to the box")
	}
}

func TestFlipImageOnceHookRunsInImage(t *testing.T) {
	c := imageCfg()
	c.Hooks.OnceBeforeFlip = "bin/migrate"
	f := newFakeBox(
		rule{match: "cat /opt/svc/hadi.json", out: stateJSON(4003, "old111")},
		rule{match: "grep -c '^PORT='", out: "0"},
	)
	if err := flip(f, c, "new222", "tester", true); err != nil {
		t.Fatalf("flip: %v", err)
	}
	found := false
	for _, cmd := range f.ran {
		if strings.Contains(cmd, "podman run") && strings.Contains(cmd, "localhost/svc:new222") &&
			strings.Contains(cmd, `--entrypoint /bin/sh`) && strings.Contains(cmd, `"bin/migrate"`) {
			found = true
		}
		if strings.Contains(cmd, "cd /opt/svc/current && sudo") {
			t.Error("image once-hook ran box-side")
		}
	}
	if !found {
		t.Errorf("once-hook not run in-image; ran:\n%s", strings.Join(f.ran, "\n"))
	}
}

func TestFlipLedgerRecordsKind(t *testing.T) {
	c := imageCfg()
	f := newFakeBox(
		rule{match: "cat /opt/svc/hadi.json", out: stateJSON(4003, "old111")},
		rule{match: "grep -c '^PORT='", out: "0"},
	)
	if err := flip(f, c, "new222", "tester", false); err != nil {
		t.Fatalf("flip: %v", err)
	}
	found := false
	for _, cmd := range f.ran {
		if strings.Contains(cmd, "releases.log") && strings.Contains(cmd, "'image'") {
			found = true
		}
	}
	if !found {
		t.Error("ledger line missing the kind column")
	}
}

func TestGuardEnvImageRefusesQuotedValues(t *testing.T) {
	c := imageCfg()
	f := newFakeBox(
		rule{match: "grep -c '^PORT='", out: "0"},
		rule{match: "grep -nE", out: `3:FOO="a b"`},
	)
	err := guardEnv(f, c)
	if err == nil || !strings.Contains(err.Error(), "quoted") {
		t.Fatalf("want quoted-value refusal, got %v", err)
	}
	// Plain kinds keep systemd's forgiving parsing; no lint.
	if err := guardEnv(newFakeBox(rule{match: "grep -c '^PORT='", out: "0"}, rule{match: "grep -nE", out: `3:FOO="a b"`}), testCfg()); err != nil {
		t.Errorf("plain kind must not lint quoting: %v", err)
	}
}

func TestPlaceImageLoadsAndTags(t *testing.T) {
	c := imageCfg()
	f := newFakeBox()
	tmp := t.TempDir() + "/img.tzst"
	if err := os.WriteFile(tmp, []byte("fake-image-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := placeImage(f, c, "abc1234", tmp); err != nil {
		t.Fatalf("placeImage: %v", err)
	}
	if f.pushes["/tmp/svc-abc1234.tzst"] != "fake-image-bytes" {
		t.Error("image tarball not streamed to the box")
	}
	found := false
	for _, cmd := range f.ran {
		if strings.Contains(cmd, "podman load") &&
			strings.Contains(cmd, "podman tag \"$LOADED\" localhost/svc:abc1234") &&
			strings.Contains(cmd, "podman tag localhost/svc:abc1234 localhost/svc:current") {
			found = true
		}
	}
	if !found {
		t.Errorf("load+tag script wrong; ran:\n%s", strings.Join(f.ran, "\n"))
	}
}

func TestPruneImageIsLedgerDriven(t *testing.T) {
	f := newFakeBox()
	pruneArtifacts(f, imageCfg())
	found := false
	for _, cmd := range f.ran {
		if strings.Contains(cmd, `$5=="image"`) && strings.Contains(cmd, "tail -5") &&
			strings.Contains(cmd, "podman image prune -f") && strings.Contains(cmd, `[ "$t" = "current" ] && continue`) {
			found = true
		}
	}
	if !found {
		t.Errorf("image prune must derive keep-set from the ledger and spare :current; ran:\n%s", strings.Join(f.ran, "\n"))
	}
}

func TestRestoreCmdKindBoundary(t *testing.T) {
	// Image config + target recorded as image → retag command.
	f := newFakeBox(rule{match: "awk -F", out: "image"})
	cmd, err := restoreCmd(f, imageCfg(), "abc1234")
	if err != nil || !strings.Contains(cmd, "podman tag localhost/svc:abc1234 localhost/svc:current") {
		t.Errorf("image restore = %q, %v", cmd, err)
	}
	// Image config + tarball-era sha (legacy 4-col line → empty kind) → refuse with instructions.
	f = newFakeBox(rule{match: "awk -F", out: ""})
	_, err = restoreCmd(f, imageCfg(), "old1111")
	if err == nil || !strings.Contains(err.Error(), "deploy.json") {
		t.Errorf("want cross-kind refusal pointing at deploy.json, got: %v", err)
	}
	// Plain config + image-era sha → refuse in the other direction.
	f = newFakeBox(rule{match: "awk -F", out: "image"})
	_, err = restoreCmd(f, testCfg(), "img5555")
	if err == nil || !strings.Contains(err.Error(), "image") {
		t.Errorf("want reverse cross-kind refusal, got: %v", err)
	}
	// Plain config + legacy sha → today's binary restore, untouched.
	f = newFakeBox(rule{match: "awk -F", out: ""})
	cmd, err = restoreCmd(f, testCfg(), "bin7777")
	if err != nil || !strings.Contains(cmd, "install -m 0755") {
		t.Errorf("legacy restore changed: %q, %v", cmd, err)
	}
}
