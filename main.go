// berth is an SSH client for the terminal: a host list over ~/.ssh/config
// that connects, opens sessions as slosh panes, and edits the file in place.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"

	tea "charm.land/bubbletea/v2"

	"github.com/axel-eck/berth/internal/probe"
	"github.com/axel-eck/berth/internal/session"
	"github.com/axel-eck/berth/internal/slosh"
	"github.com/axel-eck/berth/internal/sshconf"
	"github.com/axel-eck/berth/internal/ui"
)

var version = "dev"

func main() {
	cfg := flag.String("config", sshconf.DefaultPath(), "ssh config file to manage")
	showVersion := flag.Bool("version", false, "print the version")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: berth [-config FILE] [ls | connect ALIAS]\n\n  ls              print the host aliases, one per line\n  connect ALIAS   open one session and exit when it ends\n\n")
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
