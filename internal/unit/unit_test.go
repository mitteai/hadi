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
