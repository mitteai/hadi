package main

// hadi deploy: build → ship → ensure → hooks → flip, per box, sequentially.
// hadi check: the same plan, printed, touching nothing.

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/mitteai/hadi/internal/config"
	"github.com/mitteai/hadi/internal/discover"
	"github.com/mitteai/hadi/internal/sshx"
	"github.com/mitteai/hadi/internal/ui"
	"github.com/mitteai/hadi/internal/unit"
)

func cmdDeploy(hostFlag, sshKeyFlag string, skipBuild bool) {
	c, err := config.Load("deploy.json")
	if err != nil {
		ui.Usage("%v", err)
	}
	sha := gitSHA()

	if c.Build != "" && !skipBuild {
		t := time.Now()
		build := exec.Command("sh", "-c", c.Build)
		build.Stdout, build.Stderr = os.Stdout, os.Stderr
		if err := build.Run(); err != nil {
			ui.Fail("build failed: %v", err)
		}
		ui.Say("built %s in %.1fs", c.Artifact, time.Since(t).Seconds())
	}
	artifact, err := os.ReadFile(c.Artifact)
	if err != nil {
		ui.Fail("artifact: %v (build it, or drop --skip-build)", err)
	}
	extraUnits, err := loadExtraUnits(c)
	if err != nil {
		ui.Fail("%v", err)
	}
	files := map[string][]byte{}
	for local, remote := range c.Files {
		raw, err := os.ReadFile(local)
		if err != nil {
			ui.Fail("files[%s]: %v", local, err)
		}
		files[remote] = raw
	}

	ctx, err := resolve("", "", hostFlag, sshKeyFlag)
	if err != nil {
		ui.Usage("%v", err)
	}
	defer ctx.close()
	ctx.cfg = c

	ui.Say("%s %s → %d box(es) (%s)\n", c.Name, sha, len(ctx.boxes), strings.Join(ctx.boxes, ", "))
	start := time.Now()

	err = ctx.eachBox(func(cl *sshx.Client, first bool) error {
		if err := lock(cl, c.Name); err != nil {
			return err
		}
		defer unlock(cl, c.Name)

		if err := ensureBox(cl, c); err != nil {
			return err
		}
		if err := placeArtifact(cl, c, sha, artifact); err != nil {
			return err
		}
		for remote, raw := range files {
			if err := cl.Push(raw, remote, "0644"); err != nil {
				return err
			}
		}
		if len(files) > 0 {
			ui.Step(cl.Addr(), "files", fmt.Sprintf("%d shipped", len(files)), 0, true)
		}
		if err := installUnits(cl, c, extraUnits); err != nil {
			return err
		}
		if c.Hooks.BeforeStart != "" {
			t := time.Now()
			if out, err := cl.Run(c.Hooks.BeforeStart); err != nil {
				return fmt.Errorf("before_start hook: %w\n%s", err, out)
			}
			ui.Step(cl.Addr(), "hook", "before_start: "+truncate(c.Hooks.BeforeStart, 40), time.Since(t), true)
		}
		if err := flip(cl, c, sha, deployer(), first); err != nil {
			return err
		}
		pruneArtifacts(cl, c)
		return nil
	})
	if err != nil {
		ui.Fail("\ndeploy failed · service is HEALTHY on the previous version · fix, then re-run\n%v", err)
	}
	ui.Say("\ndeployed %s in %.1fs · rollback: hadi rollback", sha, time.Since(start).Seconds())
}

// placeArtifact ships the new code. Binaries: sha-tagged copy + install onto
// the exec path (the running old color keeps its inode). Releases: unpack to
// releases/<sha> and repoint the current symlink (the running old color keeps
// its resolved dir).
func placeArtifact(cl box, c *config.Config, sha string, artifact []byte) error {
	t := time.Now()
	size := fmt.Sprintf("%.1fMB", float64(len(artifact))/1e6)

	if c.IsRelease() {
		tmp := fmt.Sprintf("/tmp/%s-%s.tgz", c.Name, sha)
		if err := cl.Push(artifact, tmp, "0644"); err != nil {
			return err
		}
		dir := fmt.Sprintf("/opt/%s/releases/%s", c.Name, sha)
		script := fmt.Sprintf(`set -e
rm -rf %[1]s && mkdir -p %[1]s
tar -xzf %[2]s -C %[1]s --strip-components=1
chown -R %[3]s %[1]s && rm %[2]s
ln -sfn %[1]s /opt/%[4]s/current`, dir, tmp, c.Run.User, c.Name)
		if out, err := cl.Run(script); err != nil {
			return fmt.Errorf("unpack release: %w\n%s", err, out)
		}
	} else {
		tagged := fmt.Sprintf("/opt/%s/bin/%s-%s", c.Name, c.Name, sha)
		if err := cl.Push(artifact, tagged, "0755"); err != nil {
			return err
		}
		if out, err := cl.Run(fmt.Sprintf("chown %s %s && install -m 0755 -o %s %s %s",
			c.Run.User, tagged, c.Run.User, tagged, c.Run.Exec)); err != nil {
			return fmt.Errorf("install binary: %w\n%s", err, out)
		}
	}
	ui.Step(cl.Addr(), "ship", fmt.Sprintf("artifact %s (%s)", sha, size), time.Since(t), true)
	return nil
}

// pruneArtifacts keeps the last 5 sha-tagged artifacts; retention equals
// rollback depth. Best-effort.
func pruneArtifacts(cl box, c *config.Config) {
	if c.IsRelease() {
		_, _ = cl.Run(fmt.Sprintf("ls -1t /opt/%s/releases | tail -n +6 | xargs -r -I{} rm -rf /opt/%s/releases/{}", c.Name, c.Name))
	} else {
		_, _ = cl.Run(fmt.Sprintf("ls -1t /opt/%s/bin/%s-* 2>/dev/null | tail -n +6 | xargs -r rm -f", c.Name, c.Name))
	}
}

func cmdCheck() {
	c, err := config.Load("deploy.json")
	if err != nil {
		ui.Usage("%v", err)
	}
	ui.Say("service   %s (zone %s)", c.Name, c.Zone)
	if c.Entry.Domain != "" {
		ui.Say("entry     https://%s (caddy terminates TLS, auto-renewed)", c.Entry.Domain)
	} else {
		ui.Say("entry     :%d (LB terminates TLS)", c.Entry.Port)
	}
	ui.Say("colors    %d / %d   health %s   ready_timeout %ds   stop_timeout %ds",
		c.Colors[0], c.Colors[1], c.Health, c.Run.ReadyTimeout, c.Run.StopTimeout)
	if len(c.Hosts) > 0 {
		ui.Say("boxes     %s (static hosts)", strings.Join(c.Hosts, ", "))
	} else if boxes, err := discoverBoxes(c); err == nil {
		ui.Say("boxes     %s (via %s)", strings.Join(boxes, ", "), c.BoxesFQDN())
	} else {
		ui.Say("boxes     unresolved: %v", err)
	}
	if c.Run.UnitFile != "" {
		ui.Say("unit      %s (hand-written, generation bypassed)", c.Run.UnitFile)
	} else {
		ui.Say("unit      generated %s:\n", c.TemplateUnitName())
		fmt.Print(indent(unit.Render(c)))
	}
	// Sanity: referenced local paths must exist (build may not have run yet,
	// so the artifact itself is allowed to be absent).
	problems := 0
	for local := range c.Files {
		if _, err := os.Stat(local); err != nil {
			ui.Say("problem   files: %s does not exist", local)
			problems++
		}
	}
	if c.ExtraUnits != "" {
		if _, err := os.Stat(c.ExtraUnits); err != nil {
			ui.Say("problem   extra_units: %s does not exist", c.ExtraUnits)
			problems++
		}
	}
	if problems > 0 {
		ui.Usage("\n%d problem(s)", problems)
	}
	ui.Say("\nok")
}

func discoverBoxes(c *config.Config) ([]string, error) {
	return discover.Boxes(c.Name, c.Zone, c.Hosts)
}

func indent(s string) string {
	var b strings.Builder
	for _, l := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		b.WriteString("  " + l + "\n")
	}
	return b.String()
}
