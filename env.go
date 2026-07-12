package main

// hadi env: the box is the source of truth, hadi is the courier.
// edit / set / unset / push / pull. Changes apply with a zero-downtime flip.

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/mitteai/hadi/internal/sshx"
	"github.com/mitteai/hadi/internal/ui"
)

func envPath(name string) string { return "/etc/" + name + "/env" }

func cmdEnv(args []string, service, zone, hostFlag, sshKeyFlag string) {
	if len(args) == 0 {
		ui.Usage("usage: hadi env edit|set|unset|push|pull ...")
	}
	verb, rest := args[0], args[1:]

	ctx, err := resolve(service, zone, hostFlag, sshKeyFlag)
	if err != nil {
		ui.Usage("%v", err)
	}
	defer ctx.close()
	c := ctx.cfg

	pullAll := func() (map[string]string, error) {
		outs := map[string]string{}
		for _, host := range ctx.boxes {
			cl, err := ctx.dial(host)
			if err != nil {
				return nil, err
			}
			out, err := cl.Run("cat " + envPath(c.Name) + " 2>/dev/null || true")
			if err != nil {
				return nil, err
			}
			outs[host] = out
		}
		return outs, nil
	}

	// shipEnv pushes content to every box and flips each one so the new env
	// takes effect with zero downtime, verified like a code deploy.
	shipEnv := func(content string) {
		if !strings.HasSuffix(content, "\n") && content != "" {
			content += "\n"
		}
		for _, line := range strings.Split(content, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), c.Run.PortEnv+"=") {
				ui.Fail("refusing to ship an env that sets %s: the unit injects the per-color port, and an env-file value would override it and break blue-green", c.Run.PortEnv)
			}
		}
		err := ctx.eachBox(func(cl *sshx.Client, first bool) error {
			if err := lock(cl, c.Name); err != nil {
				return err
			}
			defer unlock(cl, c.Name)
			if err := cl.Push([]byte(content), envPath(c.Name), "0640"); err != nil {
				return err
			}
			if out, err := cl.Run(fmt.Sprintf("chown %s %s", c.Run.User, envPath(c.Name))); err != nil {
				return fmt.Errorf("%w\n%s", err, out)
			}
			ui.Step(cl.Host, "env", "shipped, flipping colors", 0, true)
			return flip(cl, c, "env-change", deployer(), false)
		})
		if err != nil {
			ui.Fail("\nenv change failed · old env still serving\n%v", err)
		}
		ui.Say("\nenv live on all boxes (zero downtime)")
	}

	switch verb {
	case "pull":
		outs, err := pullAll()
		if err != nil {
			ui.Fail("%v", err)
		}
		warnDrift(outs)
		if len(rest) == 1 {
			if err := os.WriteFile(rest[0], []byte(outs[ctx.boxes[0]]), 0o600); err != nil {
				ui.Fail("%v", err)
			}
			ui.Say("pulled %s → %s", c.Name, rest[0])
		} else {
			fmt.Print(outs[ctx.boxes[0]])
		}

	case "push":
		if len(rest) != 1 {
			ui.Usage("usage: hadi env push [-s <service>] <file>   (full replace, never a merge)")
		}
		raw, err := os.ReadFile(rest[0])
		if err != nil {
			ui.Fail("%v", err)
		}
		shipEnv(string(raw))

	case "set", "unset":
		if len(rest) == 0 {
			ui.Usage("usage: hadi env %s KEY[=VALUE] ...", verb)
		}
		outs, err := pullAll()
		if err != nil {
			ui.Fail("%v", err)
		}
		warnDrift(outs)
		current := outs[ctx.boxes[0]]
		if verb == "set" {
			current = envSet(current, rest)
		} else {
			current = envUnset(current, rest)
		}
		shipEnv(current)

	case "edit":
		outs, err := pullAll()
		if err != nil {
			ui.Fail("%v", err)
		}
		warnDrift(outs)
		before := outs[ctx.boxes[0]]
		tmp, err := os.CreateTemp("", c.Name+"-env-*.env")
		if err != nil {
			ui.Fail("%v", err)
		}
		defer os.Remove(tmp.Name())
		_, _ = tmp.WriteString(before)
		_ = tmp.Close()

		editor := os.Getenv("EDITOR")
		if editor == "" {
			editor = "vi"
		}
		ed := exec.Command("sh", "-c", editor+" "+tmp.Name())
		ed.Stdin, ed.Stdout, ed.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := ed.Run(); err != nil {
			ui.Fail("editor: %v", err)
		}
		after, err := os.ReadFile(tmp.Name())
		if err != nil {
			ui.Fail("%v", err)
		}
		if string(after) == before {
			ui.Say("no changes · nothing shipped")
			return
		}
		shipEnv(string(after))

	default:
		ui.Usage("unknown env verb %q (edit|set|unset|push|pull)", verb)
	}
}

// envSet applies KEY=VALUE pairs: replace in place, append when new.
func envSet(content string, pairs []string) string {
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = nil
	}
	for _, pair := range pairs {
		key, _, ok := strings.Cut(pair, "=")
		if !ok || key == "" {
			ui.Usage("env set: %q is not KEY=VALUE", pair)
		}
		replaced := false
		for i, l := range lines {
			if strings.HasPrefix(l, key+"=") {
				lines[i] = pair
				replaced = true
				break
			}
		}
		if !replaced {
			lines = append(lines, pair)
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

// envUnset removes keys.
func envUnset(content string, keys []string) string {
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	var out []string
	for _, l := range lines {
		drop := false
		for _, k := range keys {
			if strings.HasPrefix(l, k+"=") {
				drop = true
				break
			}
		}
		if !drop && l != "" {
			out = append(out, l)
		}
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n") + "\n"
}
