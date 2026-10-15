// spore-demo — a live dashboard for spore.host demos.
//
//	spore-demo [up]   tmux layout: dashboard on top, a local shell below
//	spore-demo dash   just the dashboard (in the current terminal)
//
// Add --sim to run the built-in simulated demo instead of reading EC2.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/scttfrdmn/spore-demo/internal/launch"
	"github.com/scttfrdmn/spore-demo/internal/source"
	"github.com/scttfrdmn/spore-demo/internal/state"
	"github.com/scttfrdmn/spore-demo/internal/ui"
)

type opts struct {
	region, profile, session string
	sim, fresh, all, login   bool
	speed                    float64
	dashHeight               int
}

func (o *opts) register(fs *flag.FlagSet) {
	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = "us-west-2"
	}
	fs.StringVar(&o.region, "region", region, "AWS region")
	fs.StringVar(&o.profile, "profile", os.Getenv("AWS_PROFILE"), "AWS profile (default $AWS_PROFILE)")
	fs.BoolVar(&o.all, "all", false, "show every spawn-managed instance, not just ones launched since start")
	fs.BoolVar(&o.sim, "sim", false, "run the simulated demo (no AWS)")
	fs.Float64Var(&o.speed, "speed", 1, "simulator speed multiplier")
	fs.StringVar(&o.session, "session", "spore", "tmux session name")
	fs.BoolVar(&o.fresh, "fresh", false, "replace an existing tmux session")
	fs.IntVar(&o.dashHeight, "dash-height", 14, "dashboard rows")
	fs.BoolVar(&o.login, "login-shell", false, "use your own login shell in the bottom pane instead of a clean demo prompt")
}

func main() {
	cmd := "up"
	args := os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	var o opts
	fset := flag.NewFlagSet("spore-demo "+cmd, flag.ExitOnError)
	o.register(fset)
	fset.Parse(args)

	var err error
	switch cmd {
	case "up":
		err = up(o)
	case "dash":
		err = dash(o)
	case "help", "-h", "--help":
		fmt.Println("usage: spore-demo [up|dash] [flags]")
		fset.PrintDefaults()
	default:
		err = fmt.Errorf("unknown command %q (want up or dash)", cmd)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore-demo:", err)
		os.Exit(1)
	}
}

func up(o opts) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	// Pass everything explicitly: panes are started by the tmux server, which
	// may predate this shell and not have its environment (e.g. AWS_PROFILE).
	dashArgs := []string{exe, "dash", "--region", o.region, "--profile", o.profile}
	if o.all {
		dashArgs = append(dashArgs, "--all")
	}
	if o.sim {
		dashArgs = append(dashArgs, "--sim", "--speed", fmt.Sprint(o.speed))
	}
	// spawn and truffle run locally, so the shell pane is this machine with
	// the same AWS profile and region the dashboard watches.
	shell := "export AWS_REGION=" + launch.Quote(o.region) + "; "
	if o.profile != "" {
		shell += "export AWS_PROFILE=" + launch.Quote(o.profile) + "; "
	}
	if o.login {
		shell += `exec "${SHELL:-/bin/sh}" -l`
	} else {
		// A neutral prompt for the audience (no personal theme, git status or
		// clock), keeping Homebrew on PATH for spawn and truffle.
		shell += `export PATH=/opt/homebrew/bin:/usr/local/bin:$PATH BASH_SILENCE_DEPRECATION_WARNING=1; ` +
			`export PS1='\[\e[1;36m\]laptop\[\e[0m\] \$ '; exec bash --noprofile --norc -i`
	}
	return launch.Run(launch.Layout{Session: o.session, DashArgs: dashArgs, ShellCmd: shell,
		DashHeight: o.dashHeight, Fresh: o.fresh})
}

func dash(o opts) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var src state.Source
	if o.sim {
		src = source.NewSim(o.speed)
	} else {
		l, err := source.NewLive(ctx, o.region, o.profile, o.all)
		if err != nil {
			return err
		}
		src = l
	}
	_, err := tea.NewProgram(ui.New(src), tea.WithAltScreen()).Run()
	return err
}
