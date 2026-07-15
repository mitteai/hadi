// Package config loads and validates deploy.json — the entire per-repo
// deployment surface. Every knob has a default; validation errors name the
// field and say what's expected.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Run holds the process knobs the generated systemd unit is rendered from.
type Run struct {
	User          string            `json:"user"`
	Exec          string            `json:"exec"`
	PortEnv       string            `json:"port_env"`
	After         []string          `json:"after"`
	Requires      []string          `json:"requires"`
	StopTimeout   int               `json:"stop_timeout_sec"`
	ReadyTimeout  int               `json:"ready_timeout_sec"`
	AmbientCaps   []string          `json:"ambient_caps"`
	ReadWritePath []string          `json:"read_write_paths"`
	EnvExtra      map[string]string `json:"env_extra"`
	Delegate      []string          `json:"delegate"`
	UnitFile      string            `json:"unit_file"`
}

// Entry is where traffic comes in: an internal front port (LB terminates TLS)
// or a public domain (Caddy terminates TLS with auto-HTTPS). Exactly one.
type Entry struct {
	Port         int    `json:"port"`
	Domain       string `json:"domain"`
	BodyMax      string `json:"body_max"`
	ProxyTimeout string `json:"proxy_timeout"`
}

// Hooks are the three lifecycle extension points. Contract: hooks must be
// idempotent, because rerunning a failed deploy reruns them.
type Hooks struct {
	BeforeStart    string `json:"before_start"`
	OnceBeforeFlip string `json:"once_before_flip"`
	AfterFlip      string `json:"after_flip"`
}

// Config is deploy.json.
type Config struct {
	Name       string            `json:"name"`
	Zone       string            `json:"zone"`
	Hosts      []string          `json:"hosts"`
	Build      string            `json:"build"`
	Artifact   string            `json:"artifact"`
	Run        Run               `json:"run"`
	Entry      Entry             `json:"entry"`
	Colors     []int             `json:"colors"`
	Health     string            `json:"health"`
	Files      map[string]string `json:"files"`
	ExtraUnits string            `json:"extra_units"`
	Hooks      Hooks             `json:"hooks"`
}

// Load reads, validates, and applies defaults.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var c Config
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	c.ApplyDefaults()
	return &c, nil
}

// Validate checks everything that has no sane default.
func (c *Config) Validate() error {
	var errs []string
	add := func(f, msg string) { errs = append(errs, fmt.Sprintf("%s: %s", f, msg)) }

	if c.Name == "" {
		add("name", "required: the service name (owns /opt/<name>, /etc/<name>/env, unit names)")
	}
	if c.Zone == "" {
		add("zone", "required: the DNS zone discovery records live under (the one key with no default)")
	}
	if c.Entry.Port == 0 && c.Entry.Domain == "" {
		add("entry", `required: {"port": N} behind an LB, or {"domain": "x.example.com"} for public TLS`)
	}
	if c.Entry.Port != 0 && c.Entry.Domain != "" {
		add("entry", "port and domain are mutually exclusive: pick where traffic enters")
	}
	if c.Run.PortEnv == "" {
		add("run.port_env", "required: the env var your service reads its listen port from")
	}
	if len(c.Colors) != 0 && len(c.Colors) != 2 {
		add("colors", "exactly two internal ports when set; omit for defaults")
	}
	if c.IsImage() && c.Run.Exec != "" {
		add("run.exec", "meaningless for image artifacts: the container runs its own CMD/ENTRYPOINT")
	}
	if c.IsImage() && c.ImageRef() == "" {
		add("artifact", `image artifacts are "image:<local tag>", e.g. "image:mitte:release"`)
	}
	if len(errs) > 0 {
		return fmt.Errorf("deploy.json invalid:\n  %s", strings.Join(errs, "\n  "))
	}
	return nil
}

// ApplyDefaults fills every optional knob. Config states only what deviates.
func (c *Config) ApplyDefaults() {
	if c.Run.User == "" {
		c.Run.User = c.Name
	}
	if c.Run.Exec == "" && !c.IsImage() {
		c.Run.Exec = fmt.Sprintf("/opt/%s/bin/%s", c.Name, c.Name)
	}
	if c.Run.StopTimeout == 0 {
		c.Run.StopTimeout = 90
	}
	if c.Run.ReadyTimeout == 0 {
		c.Run.ReadyTimeout = 60
	}
	if c.Health == "" {
		c.Health = "/healthz"
	}
	if len(c.Colors) == 0 {
		if c.Entry.Port != 0 {
			c.Colors = []int{c.Entry.Port + 1, c.Entry.Port + 2}
		} else {
			c.Colors = []int{4001, 4002}
		}
	}
}

// PeekZone reads only the zone from a deploy.json, without validating the
// rest. Used by fleet commands (ls, -s ...) to infer the zone from whatever
// repo you happen to be standing in.
func PeekZone(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var partial struct {
		Zone string `json:"zone"`
	}
	if json.Unmarshal(raw, &partial) != nil {
		return ""
	}
	return partial.Zone
}

// PeekName reads only the name, leniently (see PeekZone).
func PeekName(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var partial struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(raw, &partial) != nil {
		return ""
	}
	return partial.Name
}

// PeekHosts reads only the hosts override, leniently.
func PeekHosts(path string) []string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var partial struct {
		Hosts []string `json:"hosts"`
	}
	if json.Unmarshal(raw, &partial) != nil {
		return nil
	}
	return partial.Hosts
}

// BoxesFQDN is the discovery name: <name>.boxes.<zone>.
func (c *Config) BoxesFQDN() string { return c.Name + ".boxes." + c.Zone }

// TemplateUnitName is the generated unit's filename.
func (c *Config) TemplateUnitName() string { return c.Name + "@.service" }

// OtherColor returns the color that isn't the given one.
func (c *Config) OtherColor(port int) int {
	if port == c.Colors[0] {
		return c.Colors[1]
	}
	return c.Colors[0]
}

// FrontPort is the port Caddy listens on for port-mode entries; 0 for domain mode.
func (c *Config) FrontPort() int { return c.Entry.Port }

// IsRelease reports whether the artifact is a tarball release (unpacked per
// deploy) rather than a single binary.
func (c *Config) IsRelease() bool {
	return strings.HasSuffix(c.Artifact, ".tgz") || strings.HasSuffix(c.Artifact, ".tar.gz")
}

// IsImage reports whether the artifact is a container image ("image:<tag>"),
// shipped via save|zstd|load and run under podman.
func (c *Config) IsImage() bool { return strings.HasPrefix(c.Artifact, "image:") }

// ImageRef is the local tag the build must produce ("image:mitte:release" →
// "mitte:release"). Box-side tags are always localhost/<name>:<sha>.
func (c *Config) ImageRef() string { return strings.TrimPrefix(c.Artifact, "image:") }

// Kind names the artifact kind for the release ledger; rollback refuses to
// cross the image boundary based on it.
func (c *Config) Kind() string {
	switch {
	case c.IsImage():
		return "image"
	case c.IsRelease():
		return "release"
	default:
		return "binary"
	}
}

// BoxImage is the box-side moving tag — the image analogue of the
// current-release symlink. localhost/ keeps podman from ever consulting a
// registry for it.
func (c *Config) BoxImage() string { return "localhost/" + c.Name }
