package ui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/axel-eck/berth/internal/session"
	"github.com/axel-eck/berth/internal/slosh"
)

type stepState int

const (
	stepPending stepState = iota
	stepActive
	stepDone
	stepFailed
)

type stepView struct {
	state  stepState
	text   string
	detail string
	attn   bool
}

type note struct {
	text string
	warn bool
}

// connect is the screen between pressing enter and getting a shell.
type connect struct {
	alias     string
	sess      *session.Session
	steps     [4]stepView
	notes     []note
	prompt    *session.Event
	input     textinput.Model
	frame     int
	failed    string
	handedOff bool
	closeIn   int // seconds left before a failed single-connection run closes its pane
}

var spinFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func newConnect(alias string, pal Palette) *connect {
	ti := textinput.New()
	ti.Prompt = ""
	ti.SetVirtualCursor(true)
	st := textinput.DefaultDarkStyles()
	st.Cursor.Color = pal.Accent
	ti.SetStyles(st)
	c := &connect{alias: alias, input: ti}
	c.steps[session.StepConnect] = stepView{state: stepActive, text: "connecting"}
	c.steps[session.StepHostKey] = stepView{text: "host key"}
	c.steps[session.StepAuth] = stepView{text: "authentication"}
	c.steps[session.StepSession] = stepView{text: "shell"}
	return c
}

type sessionStartedMsg struct {
	sess *session.Session
	err  error
}
type sessionEventMsg struct{ ev session.Event }
type sessionEndedMsg struct {
	alias   string
	elapsed time.Duration
	code    int
}
type spinMsg struct{}
type countdownMsg struct{}
type splashMsg struct{}

func splashTick() tea.Cmd {
	return tea.Tick(splashFrame, func(time.Time) tea.Msg { return splashMsg{} })
}

func countdownCmd() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return countdownMsg{} })
}

func startSessionCmd(alias string, cols, rows int) tea.Cmd {
	return func() tea.Msg {
		s, err := session.Start(alias, cols, rows)
		return sessionStartedMsg{s, err}
	}
}

func waitEventCmd(s *session.Session) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-s.Events
		if !ok {
			return nil
		}
		return sessionEventMsg{ev}
	}
}

func spinCmd() tea.Cmd {
	return tea.Tick(110*time.Millisecond, func(time.Time) tea.Msg { return spinMsg{} })
}

func (c *connect) apply(ev session.Event) {
	switch ev.Kind {
	case session.EvStep:
		for i := range c.steps {
			if session.Step(i) < ev.Step && c.steps[i].state != stepFailed {
				c.steps[i].state = stepDone
			}
		}
		st := &c.steps[ev.Step]
		st.text, st.attn = ev.Text, ev.Attn
		if ev.Detail != "" {
			st.detail = ev.Detail
		}
		if ev.Done {
			st.state = stepDone
			st.attn = false
			if int(ev.Step)+1 < len(c.steps) && c.steps[ev.Step+1].state == stepPending {
				c.steps[ev.Step+1].state = stepActive
			}
		} else {
			st.state = stepActive
		}
	case session.EvNote:
		c.notes = append(c.notes, note{ev.Text, ev.Warn})
		if len(c.notes) > 6 {
			c.notes = c.notes[1:]
		}
	case session.EvPrompt:
		ev := ev
		c.prompt = &ev
		c.input.Reset()
		if ev.Secret {
			c.input.EchoMode = textinput.EchoPassword
			c.input.EchoCharacter = '•'
		} else {
			c.input.EchoMode = textinput.EchoNormal
		}
		c.steps[ev.Step].state = stepActive
		c.steps[ev.Step].attn = true
		c.steps[ev.Step].text = ev.Text
		if ev.Detail != "" {
			c.steps[ev.Step].detail = ev.Detail
		}
	case session.EvExit:
		c.failed = "ssh exited"
		if ev.Err != nil {
			c.failed = ev.Err.Error()
		}
		c.prompt = nil
		for i := range c.steps {
			if c.steps[i].state == stepActive {
				c.steps[i].state = stepFailed
				c.steps[i].attn = false
			}
		}
	}
}

// connectLines draws the card. w is the content width available.
func (m Model) connectLines(w int) []string {
	c := m.conn
	var lines []string
	h := m.byAlias(c.alias)
	lines = append(lines, "")
	if h != nil {
		head := lipgloss.NewStyle().Bold(true).Render(h.target())
		if e := h.env(); e != nil {
			head = lipgloss.NewStyle().Foreground(e.Color).Bold(true).Reverse(true).Render(" "+e.Label+" ") + "  " + head
		}
		lines = append(lines, "  "+head)
		var meta []string
		if h.Comment != "" {
			meta = append(meta, h.Comment)
		}
		if len(h.Tags) > 0 {
			meta = append(meta, "#"+strings.Join(h.Tags, " #"))
		}
		if h.Eff != nil {
			meta = append(meta, h.authSummary())
		}
		if len(meta) > 0 {
			lines = append(lines, "  "+m.st.muted.Render(strings.Join(meta, " · ")))
		}
	} else {
		lines = append(lines, "  "+lipgloss.NewStyle().Bold(true).Render(c.alias))
	}
	lines = append(lines, "")
	for _, st := range c.steps {
		var mark, text string
		switch st.state {
		case stepDone:
			mark, text = m.st.ok.Render("✓"), st.text
		case stepActive:
			mark = m.st.accent.Render(spinFrames[c.frame%len(spinFrames)])
			if st.attn {
				text = m.st.accent.Bold(true).Render(st.text)
			} else {
				text = st.text
			}
		case stepFailed:
			mark, text = m.st.warn.Render("✗"), m.st.warn.Render(st.text)
		default:
			mark, text = m.st.muted.Render("·"), m.st.muted.Render(st.text)
		}
		line := "  " + mark + " " + text
		if st.detail != "" && st.state != stepPending {
			line += "  " + m.st.muted.Render(st.detail)
		}
		lines = append(lines, line)
	}
	lines = append(lines, "")
	if p := c.prompt; p != nil {
		if p.Choice {
			for _, l := range p.Context {
				lines = append(lines, "  "+m.st.muted.Render(l))
			}
			lines = append(lines, "", "  "+m.st.accent.Render("y")+" trust and continue   "+m.st.accent.Render("n")+" abort")
		} else {
			// What ssh printed before asking, so a prompt is never a bare
			// field with nothing to judge it by.
			for _, l := range p.Context {
				lines = append(lines, "  "+m.st.muted.Render(l))
			}
			label := p.Text
			if p.Detail != "" {
				label += " · " + p.Detail
			}
			c.input.SetWidth(max(10, w-6-lipgloss.Width(label)))
			lines = append(lines, "  "+m.st.hint.Render(label)+"  "+c.input.View())
		}
		lines = append(lines, "")
	}
	for _, n := range c.notes {
		if n.warn {
			lines = append(lines, "  "+m.st.warn.Render("! "+n.text))
		} else {
			lines = append(lines, "  "+m.st.muted.Render(n.text))
		}
	}
	if len(c.notes) > 0 {
		lines = append(lines, "")
	}
	switch {
	case c.failed != "":
		back := " back"
		if m.exitAfter {
			back = fmt.Sprintf(" close   closing in %ds", c.closeIn)
		}
		lines = append(lines, "  "+m.st.warn.Render(c.failed), "", "  "+m.st.accent.Render("enter")+m.st.muted.Render(" retry   ")+m.st.accent.Render("esc")+m.st.muted.Render(back))
	case c.sess != nil:
		lines = append(lines, "  "+m.st.muted.Render(fmt.Sprintf("%s · esc cancels", c.sess.Elapsed())))
	}
	return lines
}

func (m Model) startConnect(alias string) (Model, tea.Cmd) {
	m.conn = newConnect(alias, m.pal)
	m.mode = modeConnect
	cols, rows := m.width, m.height
	if cols == 0 {
		cols, rows = 80, 24
	}
	return m, tea.Batch(startSessionCmd(alias, cols, rows), spinCmd())
}

func (m Model) updateConnect(msg tea.Msg) (tea.Model, tea.Cmd) {
	c := m.conn
	switch msg := msg.(type) {
	case sessionStartedMsg:
		if msg.err != nil {
			c.failed = msg.err.Error()
			if m.exitAfter {
				c.closeIn = 8
				return m, countdownCmd()
			}
			return m, nil
		}
		c.sess = msg.sess
		return m, waitEventCmd(c.sess)
	case sessionEventMsg:
		if msg.ev.Text == "approve in your agent" {
			msg.ev.Text = "approve in " + m.agentName(c.alias)
		}
		c.apply(msg.ev)
		switch msg.ev.Kind {
		case session.EvReady:
			c.handedOff = true
			sess, started := c.sess, c.sess.Started
			var cue *session.Cue
			if e := m.envOf(c.alias); e != nil {
				cue = &session.Cue{Label: e.Label, Hex: e.Hex, Bg: e.Bg}
				if m.slosh != nil {
					// The pane's title says it too, from every other tab.
					m.slosh.SetPaneName(m.paneID, e.Label+" · "+c.alias)
					if e.Name == "prod" {
						m.signPane = m.openSign(e)
					}
				}
			}
			return m, tea.Exec(sess.Proxy(m.slosh != nil, cue), func(error) tea.Msg {
				return sessionEndedMsg{sess.Alias, time.Since(started).Round(time.Second), sess.ExitCode()}
			})
		case session.EvExit:
			if m.exitAfter {
				c.closeIn = 8
				return m, countdownCmd()
			}
			return m, nil
		case session.EvPrompt:
			if !msg.ev.Choice {
				return m, tea.Batch(waitEventCmd(c.sess), c.input.Focus())
			}
		}
		return m, waitEventCmd(c.sess)
	case sessionEndedMsg:
		if m.slosh != nil && m.envOf(msg.alias) != nil {
			m.slosh.SetPaneName(m.paneID, "")
		}
		if m.signPane != 0 {
			m.slosh.Close(m.signPane)
			m.signPane = 0
		}
		if m.exitAfter {
			// Keep the card up for the last frame rather than flashing the list.
			m.summary = fmt.Sprintf("berth: %s · %s", msg.alias, msg.elapsed)
			return m, tea.Quit
		}
		m.conn = nil
		m.mode = modeList
		text := fmt.Sprintf("%s: disconnected after %s", msg.alias, msg.elapsed)
		if msg.code != 0 {
			text += fmt.Sprintf(" (exit %d)", msg.code)
		}
		cmds := []tea.Cmd{m.say(text, msg.code != 0)}
		if h := m.byAlias(msg.alias); h != nil && h.Eff != nil && h.jump() == "" {
			h.Reach = reachProbing
			cmds = append(cmds, reachCmd(h.Alias, h.hostname(), h.port()))
		}
		return m, tea.Batch(cmds...)
	case countdownMsg:
		if c.failed == "" || !m.exitAfter {
			return m, nil
		}
		c.closeIn--
		if c.closeIn <= 0 {
			return m, tea.Quit
		}
		return m, countdownCmd()
	case spinMsg:
		if c.failed != "" || c.handedOff {
			return m, nil
		}
		c.frame++
		return m, spinCmd()
	case tea.KeyPressMsg:
		key := msg.String()
		if c.failed != "" {
			switch key {
			case "enter", "r":
				return m.startConnect(c.alias)
			case "esc", "q":
				return m.leaveConnect()
			}
			return m, nil
		}
		if key == "esc" {
			if c.sess != nil {
				c.sess.Kill()
			}
			return m.leaveConnect()
		}
		if p := c.prompt; p != nil && c.sess != nil {
			if p.Choice {
				switch key {
				case "y", "Y":
					c.sess.Answer("yes")
					c.prompt = nil
					c.steps[session.StepHostKey].attn = false
				case "n", "N":
					c.sess.Answer("no")
					c.prompt = nil
				}
				return m, nil
			}
			if key == "enter" {
				c.sess.Answer(c.input.Value())
				c.input.Reset() // the secret has no business staying in memory
				c.prompt = nil
				c.steps[p.Step].attn = false
				c.steps[p.Step].text = "checking"
				return m, nil
			}
			var cmd tea.Cmd
			c.input, cmd = c.input.Update(msg)
			return m, cmd
		}
		return m, nil
	}
	if c.prompt != nil {
		var cmd tea.Cmd
		c.input, cmd = c.input.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) leaveConnect() (tea.Model, tea.Cmd) {
	m.conn = nil
	if m.exitAfter {
		return m, tea.Quit
	}
	m.mode = modeList
	return m, nil
}

// Summary is printed after the program exits when berth was started for a
// single connection, so the dead slosh pane says what it was.
func (m Model) Summary() string {
	if m.summary != "" {
		return m.summary
	}
	return ""
}

// Sign float size, and the pane size that leaves room for it.
const (
	signW, signH       = 36, 15
	signMinW, signMinH = 100, 26
)

// openSign floats the rotating warning sign in this pane's corner for a
// production session, when the pane is wide enough to spare the corner.
// Returns the float's pane id, or 0.
func (m Model) openSign(e *environment) int {
	var here slosh.Pane
	var ok bool
	if m.paneID != 0 {
		here, ok = m.slosh.PaneByID(m.paneID)
	} else {
		var err error
		here, err = m.slosh.Here()
		ok = err == nil
	}
	if !ok || here.ContentW < signMinW || here.ContentH < signMinH {
		return 0
	}
	cmd := fmt.Sprintf("%s sign --label %s --color %s --watch %d --self %%d", shq(berthExecutable()), shq(e.Label), shq(e.Hex), here.ID)
	id, err := m.slosh.OpenSign(here, cmd, signW, signH)
	if err != nil {
		return 0
	}
	return id
}

// envOf is the environment of a host by alias, nil when unknown.
func (m Model) envOf(alias string) *environment {
	if h := m.byAlias(alias); h != nil {
		return h.env()
	}
	return nil
}

// agentName says which agent will be asked to approve, from the host's
// effective IdentityAgent.
func (m Model) agentName(alias string) string {
	if h := m.byAlias(alias); h != nil && h.Eff != nil {
		agent := strings.ToLower(h.Eff.Get("identityagent"))
		switch {
		case strings.Contains(agent, "1password"):
			return "1Password"
		case strings.Contains(agent, "bitwarden"):
			return "Bitwarden"
		case strings.Contains(agent, "secretive"):
			return "Secretive"
		case strings.Contains(agent, "gnupg") || strings.Contains(agent, "gpg"):
			return "gpg-agent"
		}
	}
	return "your agent"
}

// berthExecutable is how spawned panes get the same connection screen.
func berthExecutable() string {
	if exe, err := os.Executable(); err == nil {
		return exe
	}
	return "berth"
}

// shq quotes s for a POSIX shell.
func shq(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shqAll quotes each argument, with a trailing space when there are any.
func shqAll(args []string) string {
	var b strings.Builder
	for _, a := range args {
		b.WriteString(shq(a) + " ")
	}
	return b.String()
}

// spawnCommand is the shell line a slosh pane runs for alias: berth itself,
// so the pane gets the same connection screen and `r` reruns it, with the
// launching shell's PATH, ssh and agent socket carried over, since a pane
// slosh creates starts from a bare environment.
func spawnCommand(alias string) string {
	env := []string{"env", "PATH=" + shq(os.Getenv("PATH")), "BERTH_SSH=" + shq(session.SSH)}
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		env = append(env, "SSH_AUTH_SOCK="+shq(sock))
	}
	cfg := ""
	if session.ConfigFile != "" {
		cfg = "-config " + shq(session.ConfigFile) + " "
	}
	return strings.Join(env, " ") + " " + shq(berthExecutable()) + " " + cfg + "connect " + shq(alias)
}
