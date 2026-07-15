package unit

import (
	"strings"
	"testing"

	"github.com/mitteai/hadi/internal/config"
)

func videoServiceConfig() *config.Config {
	c := &config.Config{
		Name: "video-service",
		Zone: "example.com",
		Run: config.Run{
			PortEnv:       "VIDEO_PORT",
			After:         []string{"docker.service"},
			StopTimeout:   600,
			AmbientCaps:   []string{"CAP_NET_BIND_SERVICE", "CAP_NET_ADMIN"},
			ReadWritePath: []string{"/var/sandbox", "/var/run"},
			EnvExtra:      map[string]string{"VIDEO_SHUTDOWN_TIMEOUT": "10m"},
		},
		Entry: config.Entry{Port: 4002},
	}
	c.ApplyDefaults()
	return c
}

func TestRenderPortInjection(t *testing.T) {
	u := Render(videoServiceConfig())
	if !strings.Contains(u, "Environment=VIDEO_PORT=%i") {
		t.Error("port env not injected from %i")
	}
	if !strings.Contains(u, "EnvironmentFile=-/etc/video-service/env") {
		t.Error("env file missing")
	}
}

func TestRenderCapsSuppressNNP(t *testing.T) {
	u := Render(videoServiceConfig())
	if strings.Contains(u, "NoNewPrivileges") {
		t.Error("NNP must be off when ambient caps are requested (they conflict)")
	}
	if !strings.Contains(u, "AmbientCapabilities=CAP_NET_BIND_SERVICE CAP_NET_ADMIN") {
		t.Error("caps missing")
	}
}

func TestRenderNoCapsGetsNNP(t *testing.T) {
	c := &config.Config{Name: "socket", Zone: "z", Run: config.Run{PortEnv: "PORT"}, Entry: config.Entry{Domain: "socket.example.com"}}
	c.ApplyDefaults()
	u := Render(c)
	if !strings.Contains(u, "NoNewPrivileges=true") {
		t.Error("plain services should get NNP hardening")
	}
}

func TestRenderDeterministic(t *testing.T) {
	c := videoServiceConfig()
	c.Run.EnvExtra = map[string]string{"B": "2", "A": "1", "C": "3"}
	first := Render(c)
	for i := 0; i < 10; i++ {
		if Render(c) != first {
			t.Fatal("render is not deterministic across map iterations")
		}
	}
	if strings.Index(first, "Environment=A=1") > strings.Index(first, "Environment=B=2") {
		t.Error("env extra not sorted")
	}
}

func TestRenderStopTimeout(t *testing.T) {
	u := Render(videoServiceConfig())
	if !strings.Contains(u, "TimeoutStopSec=600") {
		t.Error("stop timeout not rendered")
	}
}

// systemd rejects relative ExecStart. Release tarballs declare exec relative
// to the unpacked release ("bin/server"); the render must anchor it to the
// current-release symlink. Absolute paths (the binary default) pass through.
func TestRenderRelativeExecAnchored(t *testing.T) {
	c := &config.Config{Name: "mitte", Zone: "z",
		Run:   config.Run{PortEnv: "PORT", Exec: "bin/server"},
		Entry: config.Entry{Port: 4100}}
	c.ApplyDefaults()
	if u := Render(c); !strings.Contains(u, "ExecStart=/opt/mitte/current/bin/server") {
		t.Errorf("relative exec not anchored:\n%s", u)
	}

	c2 := &config.Config{Name: "socket", Zone: "z", Run: config.Run{PortEnv: "PORT"}, Entry: config.Entry{Port: 4006}}
	c2.ApplyDefaults()
	if u := Render(c2); !strings.Contains(u, "ExecStart=/opt/socket/bin/socket") {
		t.Error("default absolute exec must pass through unchanged")
	}
}

func imageConfig() *config.Config {
	c := &config.Config{
		Name:     "mitte",
		Zone:     "example.com",
		Artifact: "image:mitte:release",
		Run:      config.Run{PortEnv: "PORT", StopTimeout: 90},
		Entry:    config.Entry{Port: 4100},
	}
	c.ApplyDefaults()
	return c
}

func TestRenderImageUnit(t *testing.T) {
	u := Render(imageConfig())
	for _, want := range []string{
		"Type=notify",
		"NotifyAccess=all",
		"--sdnotify=conmon --cgroups=split --pull=never --log-driver=passthrough",
		"--network host",
		"--user {{UID}}:{{GID}}",
		"--cap-drop=all",
		"--security-opt no-new-privileges",
		"--env-file /etc/mitte/env",
		"--env PORT=%i",
		"--stop-timeout 90",
		"localhost/mitte:current",
		"ExecStop=-/usr/bin/podman stop mitte-%i",
		"TimeoutStopSec=95", // stop_timeout + 5 so podman always finishes first
		"TimeoutStartSec=30",
	} {
		if !strings.Contains(u, want) {
			t.Errorf("image unit missing %q:\n%s", want, u)
		}
	}
	// The systemd sandboxing block belongs to plain units; containers replace it.
	for _, reject := range []string{"ProtectSystem", "ProtectHome", "PrivateTmp", "NoNewPrivileges=true", "User=", "EnvironmentFile"} {
		if strings.Contains(u, reject) {
			t.Errorf("image unit must not carry %q (container boundary replaces it):\n%s", reject, u)
		}
	}
}

func TestRenderImageKnobMapping(t *testing.T) {
	c := imageConfig()
	c.Run.AmbientCaps = []string{"CAP_NET_ADMIN"}
	c.Run.ReadWritePath = []string{"/var/cache/mitte"}
	c.Run.Delegate = []string{"cpu", "memory"}
	c.Run.EnvExtra = map[string]string{"RELEASE_TMP": "/tmp"}
	u := Render(c)
	for _, want := range []string{
		"--cap-add=NET_ADMIN",
		"-v /var/cache/mitte:/var/cache/mitte",
		"Delegate=cpu memory",
		"--env RELEASE_TMP=/tmp",
	} {
		if !strings.Contains(u, want) {
			t.Errorf("knob mapping missing %q:\n%s", want, u)
		}
	}
	if strings.Contains(u, "no-new-privileges") {
		t.Error("no-new-privileges must be dropped when caps are requested (mirrors the plain branch)")
	}
}
