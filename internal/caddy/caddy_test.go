package caddy

import (
	"strings"
	"testing"

	"github.com/mitteai/hadi/internal/config"
)

func cfg(entry config.Entry) *config.Config {
	c := &config.Config{Name: "svc", Zone: "example.com", Run: config.Run{PortEnv: "PORT"}, Entry: entry}
	c.ApplyDefaults()
	return c
}

func TestRenderPortMode(t *testing.T) {
	c := cfg(config.Entry{Port: 4002})
	site := RenderSite(c, 4003)
	if !strings.Contains(site, ":4002 {") {
		t.Error("front port missing")
	}
	if !strings.Contains(site, "reverse_proxy 127.0.0.1:4003") {
		t.Error("upstream missing")
	}
}

func TestRenderDomainMode(t *testing.T) {
	c := cfg(config.Entry{Domain: "svc.example.com", BodyMax: "250MB", ProxyTimeout: "15m"})
	site := RenderSite(c, 4001)
	if !strings.Contains(site, "svc.example.com {") {
		t.Error("domain site missing (auto-HTTPS depends on it)")
	}
	if !strings.Contains(site, "max_size 250MB") || !strings.Contains(site, "read_timeout 15m") {
		t.Error("proxy knobs missing")
	}
}

func TestFlipRoundTrip(t *testing.T) {
	c := cfg(config.Entry{Port: 8080})
	c.Colors = []int{8081, 8082}
	site := RenderSite(c, 8081)
	got, err := ActiveColor(site, c)
	if err != nil || got != 8081 {
		t.Fatalf("ActiveColor = %d, %v", got, err)
	}
	flipped := RenderSite(c, 8082)
	got, err = ActiveColor(flipped, c)
	if err != nil || got != 8082 {
		t.Fatalf("after flip ActiveColor = %d, %v", got, err)
	}
}

func TestActiveColorParsesLegacyRawCaddyfile(t *testing.T) {
	// The exact shape of the pre-hadi migration Caddyfiles on live boxes.
	legacy := ":4002 {\n\treverse_proxy 127.0.0.1:4004\n}\n"
	c := cfg(config.Entry{Port: 4002})
	got, err := ActiveColor(legacy, c)
	if err != nil || got != 4004 {
		t.Fatalf("legacy parse = %d, %v", got, err)
	}
}

func TestActiveColorRejectsNoMarker(t *testing.T) {
	c := cfg(config.Entry{Port: 4002})
	if _, err := ActiveColor("irrelevant {}", c); err == nil {
		t.Error("want error when no upstream found")
	}
}
