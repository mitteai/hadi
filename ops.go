package main

// The operational commands: status, ls, logs, ssh, exec, releases, rollback,
// ensure. Thin wrappers over the same DNS + SSH plumbing as deploy.

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/mitteai/hadi/internal/config"
	"github.com/mitteai/hadi/internal/discover"
	"github.com/mitteai/hadi/internal/sshx"
	"github.com/mitteai/hadi/internal/ui"
)

// cmdBoxes lists boxes. Default: a table consistent with `hadi ls`, one row
// per box with live color and health. -q: plain addresses, script-friendly,
// no SSH at all.
func cmdBoxes(service, zoneFlag, sshKeyFlag string, quiet bool) {
	zone := zoneFor(zoneFlag)
	// Bare `hadi boxes` in a repo means this repo's service. An explicit
	// --zone means the whole fleet, even from inside a repo.
	if service == "" && zoneFlag == "" {
		service = config.PeekName("deploy.json")
	}

	var services []string
	if service != "" {
		services = []string{service}
	} else {
		if zone == "" {
			ui.Usage("hadi boxes needs a service (-s, or run inside a repo) or a zone for the whole fleet")
		}
		names, err := discover.Services(zone)
		if err != nil {
			ui.Fail("%v", err)
		}
		services = names
	}

	resolve := func(name string) []string {
		var hosts []string
		if name == config.PeekName("deploy.json") {
			hosts = config.PeekHosts("deploy.json")
		}
		boxes, err := discover.Boxes(name, zone, hosts)
		if err != nil {
			ui.Fail("%v", err)
		}
		return boxes
	}

	if quiet {
		for _, name := range services {
			for _, b := range resolve(name) {
				fmt.Println(b)
			}
		}
		return
	}

	key, err := sshx.LoadKey(sshKeyFlag)
	if err != nil {
		ui.Fail("%v", err)
	}
	fmt.Printf("%-15s %-18s %-6s %-9s %s\n", "SERVICE", "BOX", "LIVE", "SHA", "HEALTH")
	for _, name := range services {
		for _, b := range resolve(name) {
			cl, err := sshx.Dial(b, key)
			if err != nil {
				fmt.Printf("%-15s %-18s unreachable: %v\n", name, b, err)
				continue
			}
			st, _ := readState(cl, name)
			if st == nil || st.Config == nil {
				fmt.Printf("%-15s %-18s not deployed by hadi yet\n", name, b)
				cl.Close()
				continue
			}
			st.Config.ApplyDefaults()
			active, _ := activeColor(cl, st.Config)
			health := "ok"
			if _, err := cl.Run(healthCmd(st.Config, active)); err != nil {
				health = "UNHEALTHY"
			}
			fmt.Printf("%-15s %-18s %-6d %-9s %s\n", name, b, active, st.SHA, health)
			cl.Close()
		}
	}
}

func cmdStatus(service, zone, hostFlag, sshKeyFlag string) {
	ctx, err := resolve(service, zone, hostFlag, sshKeyFlag)
	if err != nil {
		ui.Usage("%v", err)
	}
	defer ctx.close()
	c := ctx.cfg

	for _, host := range ctx.boxes {
		cl, err := ctx.dial(host)
		if err != nil {
			ui.Fail("%v", err)
		}
		st, _ := readState(cl, c.Name)
		if st == nil {
			ui.Say("%s: no hadi.json (never deployed by hadi)", host)
			continue
		}
		// Caddy is the truth for the live color; hadi.json can be stale after
		// a non-hadi deploy flipped colors (same rule as the engine).
		active, _ := activeColor(cl, c)
		health := "ok"
		if _, err := cl.Run(healthCmd(c, active)); err != nil {
			health = "UNHEALTHY"
		}
		stale := ""
		if active != st.Active {
			stale = " (state from an older hadi deploy; something flipped since)"
		}
		age := "?"
		if t, err := time.Parse(time.RFC3339, st.DeployedAt); err == nil {
			age = humanAge(time.Since(t))
		}
		ui.Say("%s", host)
		ui.Say("  live color : %-6d sha : %-9s deployed : %s by %s%s", active, st.SHA, age, st.Deployer, stale)
		prev := st.PrevSHA
		if prev == "" {
			prev = "none"
		}
		ui.Say("  health     : %s (%s)  prev: %s  (hadi rollback restores)", health, c.Health, prev)
		// Box vitals: kernel counters, free to read; the cost is the SSH
		// round-trip, not the box.
		vitals, verr := cl.Run(`L=$(cut -d" " -f1-3 /proc/loadavg); C=$(nproc); M=$(free -m | awk '/^Mem:/{printf "%d/%dM",$3,$2}'); D1=$(df -h / | awk 'NR==2{print $5}'); D2=$(df -h /var 2>/dev/null | awk 'NR==2{print $5}'); echo "load $L ($C cores) · mem $M · disk / $D1 · /var $D2"`)
		if verr == nil && vitals != "" {
			ui.Say("  vitals     : %s", vitals)
		}
	}
}

func cmdLs(zone, sshKeyFlag string) {
	if zone == "" {
		ui.Usage("hadi ls needs --zone <zone> (or HADI_ZONE)")
	}
	names, err := discover.Services(zone)
	if err != nil {
		ui.Fail("%v", err)
	}
	key, err := sshx.LoadKey(sshKeyFlag)
	if err != nil {
		ui.Fail("%v", err)
	}
	fmt.Printf("%-15s %-6s %-6s %-9s %-10s %s\n", "SERVICE", "BOXES", "LIVE", "SHA", "HEALTH", "ENTRY")
	for _, name := range names {
		boxes, err := discover.Boxes(name, zone, nil)
		if err != nil {
			fmt.Printf("%-15s %s\n", name, err)
			continue
		}
		cl, err := sshx.Dial(boxes[0], key)
		if err != nil {
			fmt.Printf("%-15s %-6d unreachable: %v\n", name, len(boxes), err)
			continue
		}
		st, _ := readState(cl, name)
		if st == nil || st.Config == nil {
			fmt.Printf("%-15s %-6d not deployed by hadi yet\n", name, len(boxes))
			cl.Close()
			continue
		}
		st.Config.ApplyDefaults()
		active, _ := activeColor(cl, st.Config)
		health := "ok"
		if _, err := cl.Run(healthCmd(st.Config, active)); err != nil {
			health = "UNHEALTHY"
		}
		entry := fmt.Sprintf(":%d (lb)", st.Config.Entry.Port)
		if st.Config.Entry.Domain != "" {
			entry = "https://" + st.Config.Entry.Domain
		}
		fmt.Printf("%-15s %-6d %-6d %-9s %-10s %s\n", name, len(boxes), active, st.SHA, health, entry)
		cl.Close()
	}
}

func cmdLogs(service, zone, hostFlag, sshKeyFlag string, follow bool, lines int, unitOverride string) {
	ctx, err := resolve(service, zone, hostFlag, sshKeyFlag)
	if err != nil {
		ui.Usage("%v", err)
	}
	defer ctx.close()
	c := ctx.cfg

	unitFor := func(cl *sshx.Client) string {
		if unitOverride != "" {
			return unitOverride
		}
		active, _ := activeColor(cl, c)
		return fmt.Sprintf("%s@%d", c.Name, active)
	}

	if !follow {
		for _, host := range ctx.boxes {
			cl, err := ctx.dial(host)
			if err != nil {
				ui.Fail("%v", err)
			}
			out, _ := cl.Run(fmt.Sprintf("journalctl -u %s -n %d --no-pager -o short", unitFor(cl), lines))
			for _, l := range strings.Split(out, "\n") {
				fmt.Printf("[%s] %s\n", host, l)
			}
		}
		return
	}

	// -f: host-prefixed, interleaved as lines arrive. No timestamp merging:
	// that's Loki's job, and simplicity wins.
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, host := range ctx.boxes {
		cl, err := ctx.dial(host)
		if err != nil {
			ui.Fail("%v", err)
		}
		wg.Add(1)
		go func(cl *sshx.Client) {
			defer wg.Done()
			_ = cl.Stream(fmt.Sprintf("journalctl -u %s -n %d -f --no-pager -o short", unitFor(cl), lines),
				"["+cl.Addr()+"] ", func(line string) {
					mu.Lock()
					fmt.Println(line)
					mu.Unlock()
				})
		}(cl)
	}
	wg.Wait()
}

// cmdSSH execs the system ssh for a real interactive TTY — the one place hadi
// leans on the host ssh binary, because raw terminal plumbing is exactly the
// code we don't want to own. CI never runs this.
func cmdSSH(service, zone, hostFlag, sshKeyFlag string, arg string) {
	ctx, err := resolve(service, zone, hostFlag, sshKeyFlag)
	if err != nil {
		ui.Usage("%v", err)
	}
	ctx.close()

	target := arg
	if target == "" {
		if len(ctx.boxes) > 1 {
			ui.Say("boxes: %s", strings.Join(ctx.boxes, ", "))
			ui.Usage("several boxes; pick one: hadi ssh <box>")
		}
		target = ctx.boxes[0]
	}
	cmd := exec.Command("ssh", "-o", "StrictHostKeyChecking=no", "-o", "LogLevel=ERROR", "root@"+target)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		os.Exit(1)
	}
}

func cmdExec(service, zone, hostFlag, sshKeyFlag, command string) {
	ctx, err := resolve(service, zone, hostFlag, sshKeyFlag)
	if err != nil {
		ui.Usage("%v", err)
	}
	defer ctx.close()
	failed := false
	for _, host := range ctx.boxes {
		cl, err := ctx.dial(host)
		if err != nil {
			ui.Fail("%v", err)
		}
		out, err := cl.Run(command)
		for _, l := range strings.Split(out, "\n") {
			fmt.Printf("[%s] %s\n", host, l)
		}
		if err != nil {
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
}

func cmdReleases(service, zone, hostFlag, sshKeyFlag string) {
	ctx, err := resolve(service, zone, hostFlag, sshKeyFlag)
	if err != nil {
		ui.Usage("%v", err)
	}
	defer ctx.close()
	for _, host := range ctx.boxes {
		cl, err := ctx.dial(host)
		if err != nil {
			ui.Fail("%v", err)
		}
		out, _ := cl.Run("tail -20 /opt/" + ctx.cfg.Name + "/releases.log 2>/dev/null || echo '(no releases yet)'")
		ui.Say("%s:", host)
		fmt.Printf("  %-25s %-10s %-6s %s\n", "WHEN", "SHA", "COLOR", "BY")
		for _, l := range strings.Split(out, "\n") {
			parts := strings.Split(l, "\t")
			if len(parts) == 4 {
				fmt.Printf("  %-25s %-10s %-6s %s\n", parts[0], parts[1], parts[2], parts[3])
			} else if strings.TrimSpace(l) != "" {
				fmt.Printf("  %s\n", l)
			}
		}
	}
}

func cmdRollback(service, zone, hostFlag, sshKeyFlag, toSHA string) {
	ctx, err := resolve(service, zone, hostFlag, sshKeyFlag)
	if err != nil {
		ui.Usage("%v", err)
	}
	defer ctx.close()
	c := ctx.cfg

	err = ctx.eachBox(func(cl *sshx.Client, first bool) error {
		st, _ := readState(cl, c.Name)
		target := toSHA
		if target == "" {
			if st == nil || st.PrevSHA == "" {
				return fmt.Errorf("[%s] no previous release recorded; use --to <sha> (see hadi releases)", cl.Addr())
			}
			target = st.PrevSHA
		}
		if err := lock(cl, c.Name); err != nil {
			return err
		}
		defer unlock(cl, c.Name)

		t := time.Now()
		var restore string
		if c.IsRelease() {
			restore = fmt.Sprintf("test -d /opt/%[1]s/releases/%[2]s && ln -sfn /opt/%[1]s/releases/%[2]s /opt/%[1]s/current", c.Name, target)
		} else {
			restore = fmt.Sprintf("test -f /opt/%[1]s/bin/%[1]s-%[2]s && install -m 0755 -o %[3]s /opt/%[1]s/bin/%[1]s-%[2]s %[4]s", c.Name, target, c.Run.User, c.Run.Exec)
		}
		if out, err := cl.Run(restore); err != nil {
			return fmt.Errorf("[%s] artifact %s not on box (pruned? see hadi releases): %w\n%s", cl.Addr(), target, err, out)
		}
		ui.Step(cl.Addr(), "restore", "artifact "+target, time.Since(t), true)
		return flip(cl, c, target, deployer()+" (rollback)", false)
	})
	if err != nil {
		ui.Fail("\nrollback failed · current version still serving\n%v", err)
	}
	ui.Say("\nrolled back (zero downtime)")
}

func cmdEnsure(configPath, hostFlag, sshKeyFlag string) {
	c, err := loadConfigAt(configPath)
	if err != nil {
		ui.Usage("%v", err)
	}
	ctx, err := resolve("", "", hostFlag, sshKeyFlag)
	if err != nil {
		ui.Usage("%v", err)
	}
	defer ctx.close()
	ctx.cfg = c
	err = ctx.eachBox(func(cl *sshx.Client, first bool) error {
		return ensureBox(cl, c)
	})
	if err != nil {
		ui.Fail("%v", err)
	}
	ui.Say("ensured")
}

func humanAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
