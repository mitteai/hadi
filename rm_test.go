package main

// removeService against the fake box: teardown is destructive, so what it
// runs — and refuses to run — is pinned here.

import (
	"strings"
	"testing"
)

func TestRemoveServiceHappyPath(t *testing.T) {
	c := testCfg() // name svc, colors 4003/4004
	f := newFakeBox()
	if err := removeService(f, c); err != nil {
		t.Fatalf("removeService: %v", err)
	}
	for _, want := range []string{
		"systemctl stop svc@4003 svc@4004",
		"systemctl disable svc@4003 svc@4004",
		"is-active", // the still-running guard must ride the stop command
		"rm -f /etc/systemd/system/svc@.service",
		"systemctl daemon-reload",
		"rm -f /etc/caddy/hadi/svc.caddy",
		"reload caddy",
		"rm -rf /opt/svc /etc/svc",
	} {
		if !f.didRun(want) {
			t.Errorf("missing step: %q\nran: %s", want, strings.Join(f.ran, "\n     "))
		}
	}
	if f.didRun("podman") {
		t.Error("non-image service must not touch podman")
	}
}

func TestRemoveServiceTakesTheDeployLock(t *testing.T) {
	c := testCfg()
	f := newFakeBox()
	if err := removeService(f, c); err != nil {
		t.Fatalf("removeService: %v", err)
	}
	if !f.didRun("hadi.lock") {
		t.Error("rm must take the deploy lock so it can't race an in-flight deploy")
	}
	// Lock must be taken before anything is stopped.
	lockIdx, stopIdx := -1, -1
	for i, cmd := range f.ran {
		if strings.Contains(cmd, "hadi.lock") && lockIdx == -1 {
			lockIdx = i
		}
		if strings.Contains(cmd, "systemctl stop") && stopIdx == -1 {
			stopIdx = i
		}
	}
	if lockIdx == -1 || stopIdx == -1 || lockIdx > stopIdx {
		t.Errorf("lock (idx %d) must precede stop (idx %d)", lockIdx, stopIdx)
	}
}

func TestRemoveServiceHeldLockRefuses(t *testing.T) {
	c := testCfg()
	f := newFakeBox(
		rule{match: "hadi.lock", out: "HELD: 2026-07-20T10:00:00Z 123", err: errBoom},
	)
	if err := removeService(f, c); err == nil {
		t.Fatal("rm during a live deploy must refuse")
	}
	if f.didRun("rm -rf") {
		t.Error("nothing may be removed when the lock is held")
	}
}

func TestRemoveServiceImageKindClearsImages(t *testing.T) {
	c := testCfg()
	c.Artifact = "image:svc:release"
	f := newFakeBox()
	if err := removeService(f, c); err != nil {
		t.Fatalf("removeService: %v", err)
	}
	if !f.didRun("podman images -q localhost/svc") || !f.didRun("podman rmi -f") {
		t.Errorf("image service must remove its podman images\nran: %s", strings.Join(f.ran, "\n     "))
	}
}

func TestRemoveServiceStopFailureAborts(t *testing.T) {
	// Covers both real stop failures and the still-active guard: either way
	// the compound stop command errors, and nothing may be removed after —
	// deleting /opt under a live process leaves deleted-inode limbo.
	c := testCfg()
	f := newFakeBox(
		rule{match: "systemctl stop", out: "a color is still active after stop", err: errBoom},
	)
	if err := removeService(f, c); err == nil {
		t.Fatal("a failing stop must abort the removal")
	}
	if f.didRun("rm -rf /opt/svc") {
		t.Error("dirs must not be removed after a failed stop")
	}
}

// The name-integrity guards: the service name feeds `rm -rf /opt/<name>`,
// so an empty, traversal, or metacharacter name must refuse before ANY
// command reaches the box. These names arrive via corrupt or hand-edited
// hadi.json state, not just deploy.json (which Validate rejects at load).
func TestRemoveServiceRefusesUnsafeNames(t *testing.T) {
	for _, name := range []string{
		"",                // rm -rf /opt/ /etc/ — would destroy the box
		"x; rm -rf /",     // shell injection
		"../../home/user", // path traversal
		"a b",             // word-splits into extra rm arguments
		"UPPER",           // outside the pinned alphabet
		"-leading-dash",   // could be parsed as a flag
	} {
		c := testCfg()
		c.Name = name
		f := newFakeBox()
		if err := removeService(f, c); err == nil {
			t.Errorf("name %q: removal must refuse", name)
		}
		if len(f.ran) != 0 {
			t.Errorf("name %q: no command may reach the box, ran: %v", name, f.ran)
		}
	}
}

func TestRemoveServiceAcceptsRealisticNames(t *testing.T) {
	for _, name := range []string{"mitte", "mitte-pr-245", "socket_svc", "a"} {
		c := testCfg()
		c.Name = name
		if err := removeService(newFakeBox(), c); err != nil {
			t.Errorf("name %q: should be removable: %v", name, err)
		}
	}
}

// errBoom is a shared sentinel for scripted failures.
var errBoom = errStr("boom")

type errStr string

func (e errStr) Error() string { return string(e) }
