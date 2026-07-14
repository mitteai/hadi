// hadi — c'mon, go! One deploy tool for fleets of plain processes.
//
// hadi is Kamal for plain processes. terraform ends at a running box;
// hadi begins there. See docs/proposals/hadi-deploy-framework.md in the
// mitte docs repo for the full design.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/mitteai/hadi/internal/config"
	"github.com/mitteai/hadi/internal/ui"
)

var version = "0.1.0-dev"

const usage = `hadi — zero-downtime deploys for plain processes

Inside a service repo, commands read ./deploy.json. Anywhere else, -s <service>
resolves through DNS (needs --zone or HADI_ZONE). --host targets one box.
The only credential is the SSH key (HADI_SSH_KEY or --ssh-key).

  deploy   [--skip-build]        build, ship, verify, flip, drain
  check                          lint deploy.json and print the plan (touches nothing)
  env      edit|set|unset|push|pull   the box is the source of truth; changes flip
  releases                       the release ledger (sha, when, who)
  rollback [--to <sha>]          restore an earlier artifact, verify, flip
  status                         per box: live color, sha, health
  ls       [--zone <zone>]       the fleet at a glance (zone: flag, ./deploy.json, or HADI_ZONE)
  boxes    [-s <service>] [-q]   boxes with live color + health; -q for plain addresses
  logs     [-f] [-n N] [--unit]  journalctl for the live color, host-prefixed
  ssh      [box]                 interactive shell
  exec     '<cmd>'               run on every box, output per host
  ensure   [--config <path>]     converge caddy + dirs + site (idempotent)
  top      [-s <service>]        live dashboard: services, boxes, streaming logs (/ to filter)
  update                         update hadi itself to the latest release
  version
`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]

	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	service := fs.String("s", "", "service name (when outside its repo)")
	zone := fs.String("zone", "", "DNS zone for -s / ls (or HADI_ZONE)")
	host := fs.String("host", "", "target a single box")
	sshKey := fs.String("ssh-key", "", "SSH private key path (or HADI_SSH_KEY)")
	skipBuild := fs.Bool("skip-build", false, "deploy: use the existing artifact")
	follow := fs.Bool("f", false, "logs: follow")
	lines := fs.Int("n", 100, "logs: line count")
	unitName := fs.String("unit", "", "logs: a specific unit instead of the live color")
	quiet := fs.Bool("q", false, "boxes: plain addresses only, one per line")
	toSHA := fs.String("to", "", "rollback: target sha (default: previous)")
	configPath := fs.String("config", "deploy.json", "ensure: config path")
	// The stdlib flag package stops at the first positional, which would make
	// `hadi env push -s motion ...` silently drop -s. Interleave: collect
	// positionals and keep parsing flags around them.
	var rest []string
	for {
		_ = fs.Parse(args)
		args = fs.Args()
		if len(args) == 0 {
			break
		}
		rest = append(rest, args[0])
		args = args[1:]
	}

	z := zoneFor(*zone)

	switch cmd {
	case "deploy":
		cmdDeploy(*host, *sshKey, *skipBuild)
	case "check":
		cmdCheck()
	case "env":
		cmdEnv(rest, *service, z, *host, *sshKey)
	case "releases":
		cmdReleases(*service, z, *host, *sshKey)
	case "rollback":
		cmdRollback(*service, z, *host, *sshKey, *toSHA)
	case "status":
		cmdStatus(*service, z, *host, *sshKey)
	case "ls":
		cmdLs(z, *sshKey)
	case "boxes":
		cmdBoxes(*service, *zone, *sshKey, *quiet)
	case "logs":
		cmdLogs(*service, z, *host, *sshKey, *follow, *lines, *unitName)
	case "ssh":
		arg := ""
		if len(rest) > 0 {
			arg = rest[0]
		}
		cmdSSH(*service, z, *host, *sshKey, arg)
	case "exec":
		if len(rest) != 1 {
			ui.Usage("usage: hadi exec [-s <service>] '<cmd>'")
		}
		cmdExec(*service, z, *host, *sshKey, rest[0])
	case "ensure":
		cmdEnsure(*configPath, *host, *sshKey)
	case "top":
		cmdTop(*service, *zone, *sshKey)
	case "update":
		cmdUpdate()
	case "version", "--version", "-v":
		fmt.Println("hadi", version)
	case "help", "--help", "-h":
		fmt.Print(usage)
	default:
		fmt.Print(usage)
		os.Exit(2)
	}
}

func loadConfigAt(path string) (*config.Config, error) {
	return config.Load(path)
}
