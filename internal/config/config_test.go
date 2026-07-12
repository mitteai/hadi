package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "deploy.json")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestMinimalConfigDefaults(t *testing.T) {
	c, err := Load(write(t, `{
		"name": "pdf-service",
		"zone": "example.com",
		"build": "make build-linux",
		"artifact": "bin/pdf-service-linux",
		"run": {"port_env": "PORT"},
		"entry": {"port": 4005}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Run.User != "pdf-service" {
		t.Errorf("user default = %q, want service name", c.Run.User)
	}
	if c.Colors[0] != 4006 || c.Colors[1] != 4007 {
		t.Errorf("colors = %v, want front+1/front+2", c.Colors)
	}
	if c.Health != "/healthz" {
		t.Errorf("health default = %q", c.Health)
	}
	if c.Run.Exec != "/opt/pdf-service/bin/pdf-service" {
		t.Errorf("exec default = %q", c.Run.Exec)
	}
	if c.BoxesFQDN() != "pdf-service.boxes.example.com" {
		t.Errorf("boxes fqdn = %q", c.BoxesFQDN())
	}
}

func TestDomainEntryColorDefaults(t *testing.T) {
	c, err := Load(write(t, `{
		"name": "socket", "zone": "example.com",
		"run": {"port_env": "PORT"},
		"entry": {"domain": "socket.example.com"}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Colors[0] != 4001 || c.Colors[1] != 4002 {
		t.Errorf("domain-mode colors = %v, want 4001/4002", c.Colors)
	}
}

func TestValidationNamesTheField(t *testing.T) {
	_, err := Load(write(t, `{"name": "x", "zone": "z", "entry": {"port": 1}}`))
	if err == nil || !strings.Contains(err.Error(), "run.port_env") {
		t.Errorf("want run.port_env named in error, got: %v", err)
	}
}

func TestEntryMutuallyExclusive(t *testing.T) {
	_, err := Load(write(t, `{
		"name": "x", "zone": "z", "run": {"port_env": "PORT"},
		"entry": {"port": 1, "domain": "x.example.com"}
	}`))
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("want mutual-exclusion error, got: %v", err)
	}
}

func TestUnknownFieldRejected(t *testing.T) {
	_, err := Load(write(t, `{
		"name": "x", "zone": "z", "run": {"port_env": "PORT"},
		"entry": {"port": 1}, "colour": [1,2]
	}`))
	if err == nil {
		t.Error("unknown field silently accepted; typos must fail loudly")
	}
}

func TestReleaseDetection(t *testing.T) {
	c := &Config{Artifact: "dist/mitte.tgz"}
	if !c.IsRelease() {
		t.Error("tgz should be a release")
	}
	c.Artifact = "bin/socket-linux"
	if c.IsRelease() {
		t.Error("binary misdetected as release")
	}
}

func TestOtherColor(t *testing.T) {
	c := &Config{Colors: []int{4003, 4004}}
	if c.OtherColor(4003) != 4004 || c.OtherColor(4004) != 4003 {
		t.Error("OtherColor broken")
	}
}

func TestPeekZone(t *testing.T) {
	p := write(t, `{"zone": "example.com", "name": "x"}`)
	if z := PeekZone(p); z != "example.com" {
		t.Errorf("PeekZone = %q", z)
	}
	if z := PeekZone(p + ".missing"); z != "" {
		t.Errorf("missing file should peek empty, got %q", z)
	}
	bad := write(t, `not json`)
	if z := PeekZone(bad); z != "" {
		t.Errorf("bad json should peek empty, got %q", z)
	}
}
