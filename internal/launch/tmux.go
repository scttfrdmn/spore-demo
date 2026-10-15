// Package launch builds the tmux layout: dashboard on top, a real local shell
// (where spawn and truffle run) below. Using tmux for the shell — rather than
// emulating a terminal inside the TUI — means vim, less, colours and resize
// all behave exactly as they would in a normal terminal, which is what you
// want in front of an audience.
package launch

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

type Layout struct {
	Session    string
	DashArgs   []string // argv for the dashboard pane
	ShellCmd   string   // sh -c command for the bottom pane
	DashHeight int
	Fresh      bool
}

func tmux(args ...string) (string, error) {
	out, err := exec.Command("tmux", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tmux %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func Run(l Layout) error {
	if _, err := exec.LookPath("tmux"); err != nil {
		return fmt.Errorf("tmux not found: brew install tmux")
	}
	exists := exec.Command("tmux", "has-session", "-t", "="+l.Session).Run() == nil
	if exists && l.Fresh {
		if _, err := tmux("kill-session", "-t", "="+l.Session); err != nil {
			return err
		}
		exists = false
	}
	if !exists {
		if err := build(l); err != nil {
			return err
		}
	}
	return attach(l.Session)
}

func build(l Layout) error {
	// Pane ids (%N) rather than indexes so a user's base-index /
	// pane-base-index settings don't matter.
	dash, err := tmux("new-session", "-d", "-s", l.Session, "-x", "140", "-y", "40",
		"-P", "-F", "#{pane_id}", Quote(l.DashArgs...))
	if err != nil {
		return err
	}
	shell, err := tmux("split-window", "-v", "-t", dash, "-P", "-F", "#{pane_id}",
		"sh -c "+Quote(l.ShellCmd))
	if err != nil {
		return err
	}
	h := fmt.Sprint(l.DashHeight)
	cmds := [][]string{
		{"set-option", "-t", l.Session, "status", "off"},
		{"set-option", "-t", l.Session, "mouse", "on"},
		{"set-option", "-t", l.Session, "pane-border-style", "fg=colour8"},
		{"set-option", "-t", l.Session, "pane-active-border-style", "fg=colour8"},
		// keep a crashed dashboard's error on screen instead of closing the pane
		{"set-option", "-p", "-t", dash, "remain-on-exit", "on"},
		{"resize-pane", "-t", dash, "-y", h},
		// tmux redistributes rows on resize; pin the dashboard height
		{"set-hook", "-t", l.Session, "client-resized", "resize-pane -t " + dash + " -y " + h},
		{"set-hook", "-t", l.Session, "client-attached", "resize-pane -t " + dash + " -y " + h},
		{"select-pane", "-t", shell},
	}
	for _, c := range cmds {
		if _, err := tmux(c...); err != nil {
			return err
		}
	}
	return nil
}

func attach(session string) error {
	if os.Getenv("TMUX") != "" {
		_, err := tmux("switch-client", "-t", "="+session)
		return err
	}
	bin, _ := exec.LookPath("tmux")
	return syscall.Exec(bin, []string{"tmux", "attach-session", "-t", "=" + session}, os.Environ())
}

// Quote single-quotes each argument for /bin/sh.
func Quote(args ...string) string {
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(q, " ")
}
