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
	c := testCfg()
	f := newFakeBox(
		rule{match: "systemctl stop", out: "boom", err: errBoom},
	)
	if err := removeService(f, c); err == nil {
		t.Fatal("a failing stop must abort the removal")
	}
	if f.didRun("rm -rf /opt/svc") {
		t.Error("dirs must not be removed after a failed stop")
	}
}

// errBoom is a shared sentinel for scripted failures.
var errBoom = errStr("boom")

type errStr string

func (e errStr) Error() string { return string(e) }
