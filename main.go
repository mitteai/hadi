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
  ls       --zone <zone>         the fleet at a glance (via the _hadi TXT record)
  logs     [-f] [-n N] [--unit]  journalctl for the live color, host-prefixed
  ssh      [box]                 interactive shell
  exec     '<cmd>'               run on every box, output per host
  ensure   [--config <path>]     converge caddy + dirs + site (idempotent)
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
	toSHA := fs.String("to", "", "rollback: target sha (default: previous)")
	configPath := fs.String("config", "deploy.json", "ensure: config path")
	_ = fs.Parse(args)
	rest := fs.Args()

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
	case "update":
		cmdUpdate()
	case "version":
		fmt.Println("hadi", version)
	default:
		fmt.Print(usage)
		os.Exit(2)
	}
}

func loadConfigAt(path string) (*config.Config, error) {
	return config.Load(path)
}
