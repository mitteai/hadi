package main

// The engine tested against a fake box: the scariest code path becomes the
// best-tested one, exactly as the proposal promised. No real SSH anywhere.

import (
	"encoding/json"
	"fmt"
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
