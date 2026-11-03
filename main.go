// berth is an SSH client for the terminal: a host list over ~/.ssh/config
// that connects, opens sessions as slosh panes, and edits the file in place.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/axel-eck/berth/internal/probe"
	"github.com/axel-eck/berth/internal/session"
	"github.com/axel-eck/berth/internal/sign"
	"github.com/axel-eck/berth/internal/slosh"
	"github.com/axel-eck/berth/internal/sshconf"
	"github.com/axel-eck/berth/internal/ui"
)

var version = "dev"

func main() {
	cfg := flag.String("config", sshconf.DefaultPath(), "ssh config file to manage")
	showVersion := flag.Bool("version", false, "print the version")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: berth [-config FILE] [ls | connect ALIAS | sign]\n\n  ls              print the host aliases, one per line\n  connect ALIAS   open one session and exit when it ends\n  sign            the rotating warning sign (used by berth itself)\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if *showVersion {
		fmt.Println("berth", version)
		return
	}
	if flag.Arg(0) == "ls" {
		f, err := sshconf.Load(*cfg)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		for _, b := range f.Hosts() {
			if a := b.Alias(); a != "" {
				fmt.Println(a)
			}
		}
		return
	}

	// Pin ssh to the one on this shell's PATH; spawned panes inherit it
	// through BERTH_SSH (see ui.spawnCommand).
	sshPath := os.Getenv("BERTH_SSH")
	if sshPath == "" {
		if p, err := exec.LookPath("ssh"); err == nil {
			sshPath = p
		} else {
			sshPath = "ssh"
		}
	}
	session.SSH, probe.SSH = sshPath, sshPath
	if *cfg != sshconf.DefaultPath() {
		// An explicit file is what ssh must resolve and connect with too.
		session.ConfigFile, probe.ConfigFile = *cfg, *cfg
	}
	sshconf.Validate = probe.CheckConfig

	if flag.Arg(0) == "sign" {
		os.Exit(runSign(flag.Args()[1:]))
	}

	sl := slosh.Detect()
	pal, fixed := ui.DefaultPalette(true), false
	if sl != nil {
		if name, theme, err := sl.Theme(); err == nil {
			pal, fixed = ui.PaletteFromSlosh(name, theme), true
		}
	}
	model := ui.New(*cfg, sl, pal, fixed)
	if flag.Arg(0) == "connect" {
		if flag.Arg(1) == "" {
			flag.Usage()
			os.Exit(2)
		}
		model = model.Connect(flag.Arg(1))
	}
	final, err := tea.NewProgram(model).Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if m, ok := final.(ui.Model); ok {
		if m.Summary() != "" {
			fmt.Println(m.Summary())
		}
		// A single-connection pane has nothing left to show once ssh is
		// gone: close it rather than leave a dead pane in the layout.
		if sl != nil && m.PaneID() != 0 {
			sl.Close(m.PaneID())
		}
	}
}

// runSign is `berth sign`: the rotating warning sign a production session
// floats in its corner. It watches the session's pane and closes its own
// when that one is gone or no longer marked.
func runSign(args []string) int {
	fs := flag.NewFlagSet("sign", flag.ContinueOnError)
	label := fs.String("label", "PROD", "caption under the sign")
	colour := fs.String("color", "#ff4d4f", "hex colour")
	watch := fs.Int("watch", 0, "slosh pane id of the session to follow")
	self := fs.Int("self", 0, "slosh pane id of this sign, closed on exit")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	sl := slosh.Detect()
	m := sign.Model{Label: *label, Color: *colour}
	if sl != nil && *watch != 0 {
		m.Watch = func() bool {
			p, ok := sl.PaneByID(*watch)
			return ok && p.Alive && strings.HasPrefix(p.Status, "⚠")
		}
	}
	tea.NewProgram(m).Run()
	if sl != nil && *self != 0 {
		sl.Close(*self)
	}
	return 0
}
