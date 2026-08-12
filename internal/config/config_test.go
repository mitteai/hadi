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

// writeWithDockerfile puts a Dockerfile next to deploy.json — the trigger for
// the docker-by-default inference — and a fake docker on PATH, so inference
// tests pass on machines with any combination of engines installed.
func writeWithDockerfile(t *testing.T, content string) string {
	t.Helper()
	fakeEngine(t, "docker")
	p := write(t, content)
	df := filepath.Join(filepath.Dir(p), "Dockerfile")
	if err := os.WriteFile(df, []byte("FROM scratch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// fakeEngine makes PATH hold exactly one executable with the given name.
func fakeEngine(t *testing.T, name string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
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
	c, err := Load(writeWithDockerfile(t, `{
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
	_, err := Load(write(t, `{"name": "x", "zone": "z", "run": {"port_env": "PORT"}, "artifact": "bin/x"}`))
	if err == nil || !strings.Contains(err.Error(), "entry") {
		t.Errorf("want entry named in error, got: %v", err)
	}
}

func TestDockerfileInference(t *testing.T) {
	c, err := Load(writeWithDockerfile(t, `{
		"name": "forms", "zone": "example.com",
		"entry": {"domain": "forms.example.com"}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Inferred {
		t.Error("Inferred not set")
	}
	if !c.IsImage() || c.ImageRef() != "forms:hadi" {
		t.Errorf("artifact = %q, want image:forms:hadi", c.Artifact)
	}
	if c.Build != "docker build --platform linux/amd64 -t forms:hadi ." {
		t.Errorf("build = %q", c.Build)
	}
}

func TestInferenceFallsBackToPodman(t *testing.T) {
	fakeEngine(t, "podman") // and no docker on PATH
	p := write(t, `{
		"name": "forms", "zone": "example.com",
		"entry": {"domain": "forms.example.com"}
	}`)
	if err := os.WriteFile(filepath.Join(filepath.Dir(p), "Dockerfile"), []byte("FROM scratch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Build != "podman build --platform linux/amd64 -t forms:hadi ." {
		t.Errorf("build = %q, want podman fallback", c.Build)
	}
}

func TestNoLocalEngineNamesTheRealProblem(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // neither docker nor podman findable
	p := write(t, `{
		"name": "forms", "zone": "example.com",
		"entry": {"domain": "forms.example.com"}
	}`)
	if err := os.WriteFile(filepath.Join(filepath.Dir(p), "Dockerfile"), []byte("FROM scratch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(p)
	if err == nil || !strings.Contains(err.Error(), "neither docker nor podman") {
		t.Errorf(`want the engine error, not "nothing to ship", got: %v`, err)
	}
}

func TestPortEnvDefault(t *testing.T) {
	c, err := Load(writeWithDockerfile(t, `{
		"name": "forms", "zone": "example.com",
		"entry": {"domain": "forms.example.com"}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Run.PortEnv != "PORT" {
		t.Errorf("port_env default = %q, want PORT", c.Run.PortEnv)
	}
}

func TestExplicitArtifactWinsOverDockerfile(t *testing.T) {
	c, err := Load(writeWithDockerfile(t, `{
		"name": "forms", "zone": "example.com",
		"build": "make build-linux", "artifact": "bin/forms-linux",
		"entry": {"domain": "forms.example.com"}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Inferred || c.IsImage() || c.Artifact != "bin/forms-linux" {
		t.Errorf("explicit artifact must win: inferred=%v artifact=%q", c.Inferred, c.Artifact)
	}
	if c.Build != "make build-linux" {
		t.Errorf("explicit build must win, got %q", c.Build)
	}
}

func TestExplicitBuildAloneDisablesInference(t *testing.T) {
	// A lone build line means the user is mid-edit toward an explicit config;
	// guessing an artifact for their command would be wrong. Fail loudly.
	_, err := Load(writeWithDockerfile(t, `{
		"name": "forms", "zone": "example.com",
		"build": "make build-linux",
		"entry": {"domain": "forms.example.com"}
	}`))
	if err == nil || !strings.Contains(err.Error(), "artifact") {
		t.Errorf("want artifact error, got: %v", err)
	}
}

func TestNothingToShipError(t *testing.T) {
	_, err := Load(write(t, `{
		"name": "forms", "zone": "example.com",
		"entry": {"domain": "forms.example.com"}
	}`))
	if err == nil || !strings.Contains(err.Error(), "Dockerfile") {
		t.Errorf("want the error to teach both roads, got: %v", err)
	}
}

func TestInferredImageRejectsRunExec(t *testing.T) {
	_, err := Load(writeWithDockerfile(t, `{
		"name": "forms", "zone": "example.com",
		"run": {"exec": "bin/forms"},
		"entry": {"domain": "forms.example.com"}
	}`))
	if err == nil || !strings.Contains(err.Error(), "inferred from the Dockerfile") {
		t.Errorf("want run.exec rejection naming the inference, got: %v", err)
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

func TestImageDetection(t *testing.T) {
	c := &Config{Artifact: "image:mitte:release"}
	if !c.IsImage() || c.IsRelease() {
		t.Error("image: prefix must detect as image, not release")
	}
	if c.ImageRef() != "mitte:release" {
		t.Errorf("ImageRef = %q", c.ImageRef())
	}
	if (&Config{Artifact: "dist/mitte.tgz"}).IsImage() {
		t.Error("tgz misdetected as image")
	}
}

func TestKind(t *testing.T) {
	for artifact, want := range map[string]string{
		"image:mitte:release": "image",
		"dist/mitte.tgz":      "release",
		"bin/socket-linux":    "binary",
	} {
		if got := (&Config{Artifact: artifact}).Kind(); got != want {
			t.Errorf("Kind(%q) = %q, want %q", artifact, got, want)
		}
	}
}

func TestImageRejectsRunExec(t *testing.T) {
	// Validate must see run.exec BEFORE ApplyDefaults fills it — Load orders
	// Validate → ApplyDefaults; this test pins that ordering.
	_, err := Load(write(t, `{
		"name": "mitte", "zone": "example.com",
		"artifact": "image:mitte:release",
		"run": {"port_env": "PORT", "exec": "bin/server"},
		"entry": {"port": 4100}
	}`))
	if err == nil || !strings.Contains(err.Error(), "run.exec") {
		t.Errorf("want run.exec rejection for image artifacts, got: %v", err)
	}
}

func TestImageSkipsExecDefault(t *testing.T) {
	c, err := Load(write(t, `{
		"name": "mitte", "zone": "example.com",
		"artifact": "image:mitte:release",
		"run": {"port_env": "PORT"},
		"entry": {"port": 4100}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Run.Exec != "" {
		t.Errorf("exec default %q set for image kind; containers run their own CMD", c.Run.Exec)
	}
	if c.BoxImage() != "localhost/mitte" {
		t.Errorf("BoxImage = %q", c.BoxImage())
	}
}

func TestImageEmptyRefRejected(t *testing.T) {
	_, err := Load(write(t, `{
		"name": "x", "zone": "z", "artifact": "image:",
		"run": {"port_env": "PORT"}, "entry": {"port": 1}
	}`))
	if err == nil || !strings.Contains(err.Error(), "image:<local tag>") {
		t.Errorf("want empty image ref rejection, got: %v", err)
	}
}
