package ui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/axel-eck/berth/internal/session"
)

const fixture = `Host *
	IdentityAgent "~/Library/Group Containers/2BUA8C4S2C.com.1password/t/agent.sock"

# proxmox box
Host pve
	# tags: homelab
	HostName 10.0.0.100
	User root
	SetEnv TERM=xterm-256color

Host omni
	HostName 203.0.113.23
	User ubuntu
`

func press(m Model, keys ...string) Model {
	for _, k := range keys {
		var msg tea.KeyPressMsg
		switch k {
		case "enter":
			msg = tea.KeyPressMsg{Code: tea.KeyEnter}
		case "esc":
			msg = tea.KeyPressMsg{Code: tea.KeyEscape}
		case "tab":
			msg = tea.KeyPressMsg{Code: tea.KeyTab}
		case "down":
			msg = tea.KeyPressMsg{Code: tea.KeyDown}
		case "right":
			msg = tea.KeyPressMsg{Code: tea.KeyRight}
		default:
			r := []rune(k)[0]
			msg = tea.KeyPressMsg{Code: r, Text: k}
		}
		next, _ := m.Update(msg)
		m = next.(Model)
	}
	return m
}

func load(t *testing.T, w, h int) Model {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BERTH_SPLASH", "0")
	m := New(path, nil, DefaultPalette(true), true)
	next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	m = next.(Model)
	next, _ = m.Update(loadCmd(path)())
	return next.(Model)
}

func lines(m Model) []string { return strings.Split(m.View().Content, "\n") }

func assertGrid(t *testing.T, name string, m Model) {
	t.Helper()
	ls := lines(m)
	if len(ls) != m.height {
		t.Errorf("%s: %d lines for height %d", name, len(ls), m.height)
	}
	for i, l := range ls {
		if w := lipgloss.Width(l); w != m.width {
			t.Errorf("%s: line %d is %d wide, want %d: %q", name, i, w, m.width, ansi.Strip(l))
		}
	}
}

func TestListRendersEveryHost(t *testing.T) {
	m := load(t, 120, 30)
	plain := ansi.Strip(m.View().Content)
	for _, want := range []string{"pve", "root@10.0.0.100", "#homelab", "omni", "defaults", "proxmox box", "override"} {
		if !strings.Contains(plain, want) {
			t.Errorf("list view lacks %q:\n%s", want, plain)
		}
	}
	assertGrid(t, "list", m)
}

func TestFormHelpAndConfirmStayOnGrid(t *testing.T) {
	for _, size := range [][2]int{{120, 30}, {80, 24}, {60, 16}} {
		m := load(t, size[0], size[1])
		assertGrid(t, "list", m)
		assertGrid(t, "help", press(m, "?"))
		assertGrid(t, "confirm", press(m, "d"))
		f := press(m, "e")
		if f.mode != modeForm || f.form == nil || f.form.fields[fAlias].value() != "pve" {
			t.Fatalf("e should open the form on pve")
		}
		if f.form.fields[fAuth].value() != authInherit || f.form.fields[fTerm].value() != "xterm-256color" {
			t.Errorf("form did not read the stanza back: auth=%s term=%s", f.form.fields[fAuth].value(), f.form.fields[fTerm].value())
		}
		assertGrid(t, "form", f)
		assertGrid(t, "form-select", press(f, "tab", "tab", "tab", "tab", "tab", "right"))
	}
}

func TestConnectCardStaysOnGrid(t *testing.T) {
	for _, size := range [][2]int{{120, 30}, {80, 24}, {50, 14}} {
		m := load(t, size[0], size[1])
		m.conn = newConnect("pve", m.pal)
		m.mode = modeConnect
		for _, ev := range []session.Event{
			{Kind: session.EvStep, Step: session.StepConnect, Text: "connected", Done: true},
			{Kind: session.EvStep, Step: session.StepHostKey, Text: "host key ED25519", Detail: "SHA256:nWMyJVx9zEGElfFCrw0HokzZHZdOr0r53qCKwxZSDb8"},
			{Kind: session.EvPrompt, Step: session.StepHostKey, Text: "trust this host key?", Choice: true, Context: []string{"The authenticity of host '10.0.0.100' can't be established.", "ED25519 key fingerprint is: SHA256:nWMyJVx9zEGElfFCrw0HokzZHZdOr0r53qCKwxZSDb8"}},
			{Kind: session.EvNote, Text: "no post-quantum key exchange: the server should be upgraded", Warn: true},
		} {
			m.conn.apply(ev)
		}
		assertGrid(t, "connect-hostkey", m)
		// A short terminal cuts the card; only check content where it fits.
		tall := size[1] >= 24
		plain := ansi.Strip(m.View().Content)
		for _, want := range []string{"root@10.0.0.100", "✓ connected", "trust and continue", "post-quantum"} {
			if tall && !strings.Contains(plain, want) {
				t.Errorf("%v: connect card lacks %q", size, want)
			}
		}
		m.conn.apply(session.Event{Kind: session.EvPrompt, Step: session.StepAuth, Text: "PIN", Detail: "ED25519-SK id_ed25519_sk", Secret: true})
		m.conn.apply(session.Event{Kind: session.EvStep, Step: session.StepAuth, Text: "touch your YubiKey", Attn: true})
		assertGrid(t, "connect-pin", m)
		m.conn.apply(session.Event{Kind: session.EvExit, Err: errors.New("Permission denied (publickey)")})
		assertGrid(t, "connect-failed", m)
		if tall && !strings.Contains(ansi.Strip(m.View().Content), "Permission denied") {
			t.Errorf("%v: failure not shown", size)
		}
	}
}

func TestSpawnCommandCarriesEnvironment(t *testing.T) {
	t.Setenv("PATH", "/opt/homebrew/bin:/usr/bin")
	t.Setenv("SSH_AUTH_SOCK", "/tmp/it's a sock")
	old := session.SSH
	session.SSH = "/opt/homebrew/bin/ssh"
	t.Cleanup(func() { session.SSH = old })
	got := spawnCommand("pve")
	for _, want := range []string{"env PATH='/opt/homebrew/bin:/usr/bin'", "BERTH_SSH='/opt/homebrew/bin/ssh'", `SSH_AUTH_SOCK='/tmp/it'\''s a sock'`, "' connect 'pve'"} {
		if !strings.Contains(got, want) {
			t.Errorf("spawn command lacks %q:\n%s", want, got)
		}
	}
}

func TestFormRejectsValuesSshWouldChokeOn(t *testing.T) {
	m := press(load(t, 100, 24), "a")
	m = press(m, strings.Split("box", "")...)
	m = press(m, "tab", "tab", "tab", "tab", "tab", "tab") // to key file
	m = press(m, strings.Split("~/my key", "")...)
	if err := m.form.validate(m.hosts); err == nil {
		t.Fatal("a key file with a space must be refused: ssh fails on every host afterwards")
	}
}

func TestSplashRendersOnGrid(t *testing.T) {
	m := load(t, 100, 24)
	for motion := 0; motion < motionCount; motion++ {
		for fx := 0; fx < fxCount; fx++ {
			m.splash = &splash{motion: motion, fx: fx, seed: 42}
			m.mode = modeSplash
			for _, tt := range []float64{0, 0.3, 0.8, 1.1} {
				out := strings.Split(m.renderSplash(tt), "\n")
				if len(out) != m.height {
					t.Fatalf("motion %d fx %d t=%.1f: %d lines", motion, fx, tt, len(out))
				}
				for i, l := range out {
					if w := lipgloss.Width(l); w != m.width {
						t.Fatalf("motion %d fx %d t=%.1f: line %d is %d wide", motion, fx, tt, i, w)
					}
				}
			}
		}
	}
}

func TestFilterNarrowsAndEscClears(t *testing.T) {
	m := press(load(t, 100, 24), "/", "o", "m", "n")
	if m.current() == nil || m.current().Alias != "omni" {
		t.Fatalf("filter 'omn' should land on omni")
	}
	m = press(m, "esc")
	if m.filter.Value() != "" {
		t.Fatalf("esc should clear the filter")
	}
	m = press(m, "/", "#", "h", "o", "m", "e")
	if m.current() == nil || m.current().Alias != "pve" {
		t.Fatalf("filter '#home' should find the tagged host")
	}
}

func TestGroupingByTag(t *testing.T) {
	m := load(t, 100, 24)
	if !m.grouped {
		t.Fatal("a tagged host should turn grouping on")
	}
	var kinds []string
	for _, it := range m.items {
		if it.kind == itemHeader {
			kinds = append(kinds, "#"+it.group)
		} else {
			kinds = append(kinds, m.hosts[it.host].Alias)
		}
	}
	want := "#homelab pve #untagged omni #defaults *"
	if got := strings.Join(kinds, " "); got != want {
		t.Fatalf("grouped rows:\n got %s\nwant %s", got, want)
	}
	assertGrid(t, "grouped", m)
	m = press(m, "g", "space") // fold #homelab
	if len(m.items) != 5 || m.items[1].kind != itemHeader {
		t.Fatalf("space on a header should fold it: %d rows", len(m.items))
	}
	m = press(m, "#")
	if m.grouped || len(m.items) != 3 {
		t.Fatalf("# should turn grouping off: grouped=%v rows=%d", m.grouped, len(m.items))
	}
}

func TestAddHostWritesStanza(t *testing.T) {
	m := load(t, 100, 24)
	m = press(m, "a")
	for i, v := range []string{"bastion", "10.9.9.9", "ops", "2200"} {
		if i > 0 {
			m = press(m, "tab")
		}
		m = press(m, strings.Split(v, "")...)
	}
	m = press(m, "tab", "tab", "right") // jump (blank), auth -> 1Password
	if err := m.form.validate(m.hosts); err != nil {
		t.Fatal(err)
	}
	m.form.apply(m.file)
	got := m.file.Content()
	// The socket path depends on the OS and on 1Password being installed.
	want := "\nHost bastion\n\tHostName 10.9.9.9\n\tUser ops\n\tPort 2200\n\tIdentityAgent \"" + onePasswordSocket() + "\"\n"
	if !strings.HasSuffix(got, want) {
		t.Fatalf("stanza written:\n%s", got)
	}
}
