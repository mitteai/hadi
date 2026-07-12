package main

// Shared command context: resolve the target service (repo deploy.json or
// -s + the box's own hadi.json), discover boxes, hold SSH connections.

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/mitteai/hadi/internal/config"
	"github.com/mitteai/hadi/internal/discover"
	"github.com/mitteai/hadi/internal/sshx"
	"github.com/mitteai/hadi/internal/ui"
)

type cmdCtx struct {
	cfg   *config.Config
	boxes []string
	key   ssh.Signer
	conns map[string]*sshx.Client
}

// zoneFor resolves the zone: --zone flag, then the deploy.json of whatever
// repo you're standing in, then HADI_ZONE. Local context outranks global env,
// same as git config.
func zoneFor(zoneFlag string) string {
	if zoneFlag != "" {
		return zoneFlag
	}
	if z := config.PeekZone("deploy.json"); z != "" {
		return z
	}
	return os.Getenv("HADI_ZONE")
}

// resolve builds the context. In a repo (deploy.json present) the config is
// authoritative. With -s, boxes come from DNS and the config comes from the
// first box's hadi.json snapshot — the box describes itself.
func resolve(service, zoneFlag, hostFlag, sshKeyFlag string) (*cmdCtx, error) {
	key, err := sshx.LoadKey(sshKeyFlag)
	if err != nil {
		return nil, err
	}
	ctx := &cmdCtx{key: key, conns: map[string]*sshx.Client{}}

	if service == "" {
		if _, err := os.Stat("deploy.json"); err != nil {
			return nil, fmt.Errorf("no deploy.json here and no -s <service> given")
		}
		if ctx.cfg, err = config.Load("deploy.json"); err != nil {
			return nil, err
		}
	} else {
		zone := zoneFor(zoneFlag)
		if zone == "" {
			return nil, fmt.Errorf("-s needs a zone: pass --zone <zone> or set HADI_ZONE")
		}
		boxes, err := discover.Boxes(service, zone, nil)
		if err != nil {
			return nil, err
		}
		cl, err := ctx.dial(boxes[0])
		if err != nil {
			return nil, err
		}
		st, err := readState(cl, service)
		if err != nil {
			return nil, err
		}
		if st == nil || st.Config == nil {
			return nil, fmt.Errorf("%s has no hadi.json on %s: the box hasn't been deployed by hadi yet", service, boxes[0])
		}
		ctx.cfg = st.Config
		ctx.cfg.ApplyDefaults()
	}

	if hostFlag != "" {
		ctx.boxes = []string{hostFlag}
	} else if len(ctx.boxes) == 0 {
		if ctx.boxes, err = discover.Boxes(ctx.cfg.Name, ctx.cfg.Zone, ctx.cfg.Hosts); err != nil {
			return nil, err
		}
	}
	return ctx, nil
}

func (ctx *cmdCtx) dial(host string) (*sshx.Client, error) {
	if cl, ok := ctx.conns[host]; ok {
		return cl, nil
	}
	cl, err := sshx.Dial(host, ctx.key)
	if err != nil {
		return nil, err
	}
	ctx.conns[host] = cl
	return cl, nil
}

func (ctx *cmdCtx) close() {
	for _, cl := range ctx.conns {
		_ = cl.Close()
	}
}

// eachBox dials every box and runs fn, stopping at the first failure —
// sequential and boring, exactly as designed.
func (ctx *cmdCtx) eachBox(fn func(cl *sshx.Client, first bool) error) error {
	for i, host := range ctx.boxes {
		cl, err := ctx.dial(host)
		if err != nil {
			return err
		}
		if err := fn(cl, i == 0); err != nil {
			return err
		}
	}
	return nil
}

// gitSHA is the deploy identity: short HEAD in a repo, "dev" otherwise.
func gitSHA() string {
	out, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return "dev"
	}
	return strings.TrimSpace(string(out))
}

func deployer() string {
	if u := os.Getenv("GITHUB_ACTOR"); u != "" {
		return u + " (ci)"
	}
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "unknown"
}

// loadExtraUnits reads deploy/systemd-style unit files referenced by config.
func loadExtraUnits(c *config.Config) (map[string][]byte, error) {
	units := map[string][]byte{}
	if c.Run.UnitFile != "" {
		raw, err := os.ReadFile(c.Run.UnitFile)
		if err != nil {
			return nil, fmt.Errorf("run.unit_file: %w", err)
		}
		units["__unit_file__"] = raw
	}
	if c.ExtraUnits == "" {
		return units, nil
	}
	entries, err := os.ReadDir(c.ExtraUnits)
	if err != nil {
		return nil, fmt.Errorf("extra_units: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		// The generated template supersedes any legacy hand copy of it.
		if name == c.TemplateUnitName() {
			continue
		}
		raw, err := os.ReadFile(c.ExtraUnits + "/" + name)
		if err != nil {
			return nil, err
		}
		units[name] = raw
	}
	return units, nil
}

func warnDrift(outputs map[string]string) {
	var first string
	for _, v := range outputs {
		first = v
		break
	}
	for host, v := range outputs {
		if v != first {
			ui.Say("WARNING: env differs across boxes (%s deviates). hadi env push to realign.", host)
			return
		}
	}
}
