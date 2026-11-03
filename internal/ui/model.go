// Package ui is the Bubble Tea program: a host list that connects, opens
// slosh panes, and edits ~/.ssh/config in place.
package ui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/axel-eck/berth/internal/probe"
	"github.com/axel-eck/berth/internal/session"
	"github.com/axel-eck/berth/internal/slosh"
	"github.com/axel-eck/berth/internal/sshconf"
)

type mode int

const (
	modeSplash mode = iota
	modeList
	modeForm
	modeConfirmDelete
	modeConfirmTerm
	modeHelp
	modeConnect
)

type itemKind int

const (
	itemHost itemKind = iota
	itemHeader
)

// item is one row of the list: a host, or a group header when grouping.
type item struct {
	kind  itemKind
	host  int // index into Model.hosts
	group string
	count int
}

// Model is the whole UI state.
type Model struct {
	path      string
	file      *sshconf.File
	hosts     []*Host
	items     []item
	grouped   bool            // rows grouped under their first tag
	groupInit bool            // grouped was decided from the first load
	collapsed map[string]bool // folded groups
	cursor    int
	offset    int
	filter    textinput.Model
	filterOn  bool

	width, height int
	pal           Palette
	st            styles
	paletteFixed  bool // from slosh: the terminal's background does not override it

	slosh    *slosh.Client
	sessions map[string][]slosh.Pane

	mode       mode
	saving     bool // a save is in flight: block edits until the reload lands
	splash     *splash
	conn       *connect
	connectOn  string // alias to connect to right away (berth connect <alias>)
	exitAfter  bool   // quit when that session ends instead of showing the list
	paneID     int    // the slosh pane to close when that happens
	signPane   int    // the floating warning sign of the current session, if any
	summary    string
	form       *form
	pending    *Host // the host a confirm modal is about
	toast      string
	toastWarn  bool
	toastUntil time.Time
	loadErr    string
}

// New builds the model. pal is the starting palette; paletteFixed says it
// came from slosh and should not be replaced by the background probe.
func New(path string, sl *slosh.Client, pal Palette, paletteFixed bool) Model {
	ti := textinput.New()
	ti.Prompt = ""
	ti.Placeholder = "filter"
	ti.SetVirtualCursor(true)
	m := Model{path: path, slosh: sl, pal: pal, paletteFixed: paletteFixed, filter: ti, sessions: map[string][]slosh.Pane{}, collapsed: map[string]bool{}, mode: modeList}
	m.st = newStyles(pal)
	if os.Getenv("BERTH_SPLASH") != "0" {
		m.splash = newSplash()
		m.mode = modeSplash
	}
	m.applyInputStyles()
	return m
}

// Connect makes the program open alias at once and exit when it ends.
// Inside slosh it also remembers which pane it runs in, so that pane can
// be closed afterwards instead of lingering as a dead one.
func (m Model) Connect(alias string) Model {
	m.connectOn, m.exitAfter = alias, true
	m.splash, m.mode = nil, modeList // straight to the card
	if m.slosh != nil {
		m.paneID = m.slosh.OwnPane(slosh.Purpose(alias))
	}
	return m
}

// PaneID is the slosh pane this single-connection run lives in, or 0.
func (m Model) PaneID() int { return m.paneID }

func (m *Model) applyInputStyles() {
	st := textinput.DefaultDarkStyles()
	st.Focused.Placeholder = m.st.muted
	st.Blurred.Placeholder = m.st.muted
	st.Cursor.Color = m.pal.Accent
	m.filter.SetStyles(st)
}

// ---- messages ----

type loadedMsg struct {
	file  *sshconf.File
	hosts []*Host
	err   error
}
type effMsg struct {
	alias string
	eff   probe.Effective
}
type reachMsg struct {
	alias string
	d     time.Duration
	err   error
}
type sessionsMsg map[string][]slosh.Pane
type themeMsg struct {
	name  string
	theme map[string]string
}
type openedMsg struct {
	alias, how string
	err        error
}
type terminfoMsg struct {
	alias string
	err   error
}
type editorMsg struct{ err error }
type savedMsg struct {
	what    string
	chezmoi bool
	err     error
}
type tickMsg time.Time
type toastExpireMsg struct{}

// ---- commands ----

func loadCmd(path string) tea.Cmd {
	return func() tea.Msg {
		f, hosts, err := loadHosts(path)
		return loadedMsg{f, hosts, err}
	}
}

func resolveCmd(alias string) tea.Cmd {
	return func() tea.Msg {
		eff, err := probe.Resolve(alias)
		if err != nil {
			return nil
		}
		return effMsg{alias, eff}
	}
}

func reachCmd(alias, host, port string) tea.Cmd {
	return func() tea.Msg {
		d, err := probe.Reach(host, port, 1500*time.Millisecond)
		return reachMsg{alias, d, err}
	}
}

func sessionsCmd(sl *slosh.Client) tea.Cmd {
	return func() tea.Msg { return sessionsMsg(sl.Sessions()) }
}

// themeCmd follows a live theme switch in slosh (prefix t), so berth
// restyles within a tick instead of on the next launch.
func themeCmd(sl *slosh.Client, current string) tea.Cmd {
	return func() tea.Msg {
		if sl.ThemeName() == current {
			return nil
		}
		name, theme, err := sl.Theme()
		if err != nil {
			return nil
		}
		return themeMsg{name, theme}
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func toastCmd(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return toastExpireMsg{} })
}

func openCmd(sl *slosh.Client, how, alias string) tea.Cmd {
	return func() tea.Msg {
		command := spawnCommand(alias)
		var err error
		switch how {
		case "tab":
			err = sl.OpenTab(alias, command)
		case "split":
			err = sl.OpenSplit(alias, command, "rows")
		case "vsplit":
			err = sl.OpenSplit(alias, command, "cols")
		case "float":
			err = sl.OpenFloat(alias, command)
		}
		return openedMsg{alias, how, err}
	}
}

// terminfoCmd ships the local xterm-ghostty entry to the host, the fix for
// "SetEnv TERM=xterm-256color" workarounds.
func terminfoCmd(alias string) tea.Cmd {
	script := "set -e; printf '\\033[2mberth: installing xterm-ghostty terminfo on %s\\033[0m\\n' " + shq(alias) +
		"; infocmp -x xterm-ghostty | " + shq(session.SSH) + " " + shqAll(session.Args()) + "-o LogLevel=ERROR -- " + shq(alias) + " 'tic -x -'"
	c := exec.Command("sh", "-c", script)
	return tea.ExecProcess(c, func(err error) tea.Msg { return terminfoMsg{alias, err} })
}

func editorCmd(path string, line int) tea.Cmd {
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		editor = "nano"
	}
	args := []string{}
	// `+N` is the vi/nano family's spelling; other editors want their own.
	switch filepath.Base(strings.Fields(editor)[0]) {
	case "vi", "vim", "nvim", "vis", "nano", "pico", "micro", "emacs", "emacsclient", "ne", "joe", "mg":
		if line > 0 {
			args = append(args, "+"+strconv.Itoa(line))
		}
		args = append(args, path)
	case "code", "codium", "subl", "zed":
		if line > 0 {
			args = append(args, "-g", path+":"+strconv.Itoa(line))
		} else {
			args = append(args, path)
		}
	case "hx", "helix":
		if line > 0 {
			args = append(args, path+":"+strconv.Itoa(line))
		} else {
			args = append(args, path)
		}
	default:
		args = append(args, path)
	}
	c := exec.Command("sh", "-c", editor+` "$@"`, "editor")
	c.Args = append(c.Args, args...)
	return tea.ExecProcess(c, func(err error) tea.Msg { return editorMsg{err} })
}

// saveCmd writes the file and, when chezmoi manages it, re-adds it so the
// dotfiles source does not drift from the file just edited.
func saveCmd(f *sshconf.File, what string) tea.Cmd {
	return func() tea.Msg {
		if err := f.Save(); err != nil {
			return savedMsg{what: what, err: err}
		}
		if _, err := exec.LookPath("chezmoi"); err != nil {
			return savedMsg{what: what}
		}
		if exec.Command("chezmoi", "source-path", f.Path).Run() != nil {
			return savedMsg{what: what}
		}
		if err := exec.Command("chezmoi", "re-add", f.Path).Run(); err != nil {
			return savedMsg{what: what, err: fmt.Errorf("saved, but chezmoi re-add failed: %w", err)}
		}
		return savedMsg{what: what, chezmoi: true}
	}
}

// ---- lifecycle ----

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{loadCmd(m.path)}
	if m.splash != nil {
		cmds = append(cmds, splashTick())
	}
	if !m.paletteFixed {
		cmds = append(cmds, tea.RequestBackgroundColor)
	}
	if m.slosh != nil {
		cmds = append(cmds, sessionsCmd(m.slosh), tickCmd())
	}
	return tea.Batch(cmds...)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.clamp()
		if m.connectOn != "" && m.conn == nil {
			alias := m.connectOn
			m.connectOn = ""
			return m.startConnect(alias)
		}
		return m, nil
	case sessionStartedMsg, sessionEventMsg, sessionEndedMsg, spinMsg, countdownMsg:
		if m.conn == nil {
			// The card was left before ssh had even started: don't orphan it.
			if st, ok := msg.(sessionStartedMsg); ok && st.sess != nil {
				st.sess.Kill()
			}
			return m, nil
		}
		return m.updateConnect(msg)
	case tea.BackgroundColorMsg:
		if !m.paletteFixed {
			m.pal = DefaultPalette(msg.IsDark())
			m.st = newStyles(m.pal)
			m.applyInputStyles()
		}
		return m, nil
	case splashMsg:
		if m.splash == nil {
			return m, nil
		}
		if m.splash.done() {
			m.splash = nil
			if m.mode == modeSplash {
				m.mode = modeList
			}
			return m, nil
		}
		return m, splashTick()
	case loadedMsg:
		return m.onLoaded(msg)
	case effMsg:
		if h := m.byAlias(msg.alias); h != nil {
			h.Eff = msg.eff
			if h.jump() != "" || h.proxied() {
				// Reachable only through something else: dialling the name
				// directly would be wrong, and would leak the lookup.
				h.Reach = reachSkipped
				return m, nil
			}
			h.Reach = reachProbing
			return m, reachCmd(h.Alias, h.hostname(), h.port())
		}
		return m, nil
	case reachMsg:
		if h := m.byAlias(msg.alias); h != nil {
			if msg.err != nil {
				h.Reach, h.ReachErr = reachDown, shortErr(msg.err)
			} else {
				h.Reach, h.Latency = reachUp, msg.d
			}
		}
		return m, nil
	case sessionsMsg:
		m.sessions = msg
		return m, nil
	case themeMsg:
		m.pal = PaletteFromSlosh(msg.name, msg.theme)
		m.st = newStyles(m.pal)
		m.paletteFixed = true
		m.applyInputStyles()
		return m, nil
	case tickMsg:
		if m.slosh == nil {
			return m, nil
		}
		cmds := []tea.Cmd{sessionsCmd(m.slosh), tickCmd()}
		if m.paletteFixed {
			cmds = append(cmds, themeCmd(m.slosh, strings.TrimPrefix(m.pal.Name, "slosh:")))
		}
		return m, tea.Batch(cmds...)
	case openedMsg:
		if msg.err != nil {
			return m, m.say(shortErr(msg.err), true)
		}
		return m, tea.Batch(m.say(msg.alias+" opened in a "+msg.how, false), sessionsCmd(m.slosh))
	case terminfoMsg:
		if msg.err != nil {
			return m, m.say("terminfo push failed: "+shortErr(msg.err), true)
		}
		if h := m.byAlias(msg.alias); h != nil && h.termOverride() != "" && !h.ReadOnly {
			m.pending = h
			m.mode = modeConfirmTerm
			return m, nil
		}
		return m, m.say("xterm-ghostty installed on "+msg.alias, false)
	case editorMsg:
		if msg.err != nil {
			return m, m.say("editor: "+shortErr(msg.err), true)
		}
		return m, loadCmd(m.path)
	case savedMsg:
		m.saving = false
		if msg.err != nil {
			text := shortErr(msg.err)
			if errors.Is(msg.err, sshconf.ErrChanged) {
				text = "the file changed meanwhile: reloaded, redo the edit"
			}
			return m, tea.Batch(m.say(text, true), loadCmd(m.path))
		}
		note := msg.what
		if msg.chezmoi {
			note += " · chezmoi re-added"
		}
		return m, tea.Batch(m.say(note, false), loadCmd(m.path))
	case toastExpireMsg:
		if time.Now().After(m.toastUntil) {
			m.toast = ""
		}
		return m, nil
	case tea.MouseWheelMsg:
		if m.mode == modeList {
			if msg.Button == tea.MouseWheelUp {
				m.move(-1)
			} else if msg.Button == tea.MouseWheelDown {
				m.move(1)
			}
		}
		return m, nil
	case tea.MouseClickMsg:
		if m.mode == modeList && msg.Button == tea.MouseLeft && msg.X < m.listWidth() {
			if row := msg.Y - 3 + m.offset; row >= 0 && row < len(m.items) {
				m.cursor = row
				m.clamp()
			}
		}
		return m, nil
	case tea.KeyPressMsg:
		return m.onKey(msg)
	}
	if m.mode == modeConnect && m.conn != nil {
		return m.updateConnect(msg)
	}
	if m.mode == modeForm && m.form != nil {
		_, _, cmd := m.form.update(msg)
		return m, cmd
	}
	if m.filterOn {
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) onLoaded(msg loadedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.loadErr = msg.err.Error()
		return m, nil
	}
	m.loadErr = ""
	// Keep what was learned about hosts that are still there.
	old := map[string]*Host{}
	for _, h := range m.hosts {
		old[h.Alias] = h
	}
	var selected string
	if h := m.current(); h != nil {
		selected = h.Alias
	}
	m.file, m.hosts = msg.file, msg.hosts
	if !m.groupInit {
		// Grouping earns its headers only when somebody tagged something.
		m.groupInit = true
		for _, h := range m.hosts {
			if len(h.Tags) > 0 {
				m.grouped = true
			}
		}
	}
	var cmds []tea.Cmd
	for _, h := range m.hosts {
		if h.Wildcard {
			continue
		}
		if o, ok := old[h.Alias]; ok && o.Eff != nil && o.Block.File.Content() == h.Block.File.Content() {
			h.Eff, h.Reach, h.Latency, h.ReachErr = o.Eff, o.Reach, o.Latency, o.ReachErr
			continue
		}
		cmds = append(cmds, resolveCmd(h.Alias))
	}
	m.refilter()
	for i, it := range m.items {
		if it.kind == itemHost && m.hosts[it.host].Alias == selected {
			m.cursor = i
		}
	}
	if selected == "" {
		// First load: start on a host, not on the header above it.
		for i, it := range m.items {
			if it.kind == itemHost {
				m.cursor = i
				break
			}
		}
	}
	m.clamp()
	return m, tea.Batch(cmds...)
}

// ---- keys ----

func (m Model) onKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := k.String()
	if key == "ctrl+c" {
		return m, tea.Quit
	}
	switch m.mode {
	case modeSplash:
		// Any key cuts the greeting short; the key itself is spent on that.
		m.splash = nil
		m.mode = modeList
		return m, nil
	case modeConnect:
		if m.conn == nil {
			m.mode = modeList
			return m, nil
		}
		return m.updateConnect(k)
	case modeHelp:
		m.mode = modeList
		return m, nil
	case modeConfirmDelete:
		m.mode = modeList
		if key == "y" || key == "enter" {
			h := m.pending
			m.pending = nil
			h.Block.File.DeleteBlock(h.Block)
			m.saving = true
			return m, saveCmd(h.Block.File, "deleted "+h.Alias)
		}
		m.pending = nil
		return m, nil
	case modeConfirmTerm:
		m.mode = modeList
		h := m.pending
		m.pending = nil
		if key == "y" || key == "enter" {
			setTermOverride(h.Block, "")
			m.saving = true
			return m, saveCmd(h.Block.File, "dropped TERM override on "+h.Alias)
		}
		return m, m.say("xterm-ghostty installed on "+h.Alias, false)
	case modeForm:
		save, cancel, cmd := m.form.update(k)
		if cancel {
			m.mode, m.form = modeList, nil
			return m, nil
		}
		if save {
			if err := m.form.validate(m.hosts); err != nil {
				m.form.err = err.Error()
				return m, nil
			}
			b := m.form.apply(m.file)
			what := "saved " + b.Alias()
			if m.form.host == nil {
				what = "added " + b.Alias()
			}
			m.mode, m.form = modeList, nil
			m.saving = true
			return m, saveCmd(m.file, what)
		}
		return m, cmd
	}

	// List mode. The filter input eats keys while it has focus.
	if m.filterOn {
		switch key {
		case "esc":
			m.filterOn = false
			m.filter.Blur()
			m.filter.SetValue("")
			m.refilter()
			return m, nil
		case "enter":
			m.filterOn = false
			m.filter.Blur()
			return m, nil
		case "up", "ctrl+p":
			m.move(-1)
			return m, nil
		case "down", "ctrl+n":
			m.move(1)
			return m, nil
		}
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(k)
		m.refilter()
		return m, cmd
	}

	h := m.current()
	switch key {
	case "q", "esc":
		if key == "esc" && m.filter.Value() != "" {
			m.filter.SetValue("")
			m.refilter()
			return m, nil
		}
		return m, tea.Quit
	case "/":
		m.filterOn = true
		return m, m.filter.Focus()
	case "j", "down":
		m.move(1)
	case "k", "up":
		m.move(-1)
	case "g", "home":
		m.cursor = 0
		m.clamp()
	case "G", "end":
		m.cursor = len(m.items) - 1
		m.clamp()
	case "#":
		m.grouped = !m.grouped
		m.refilter()
		return m, nil
	case "ctrl+d", "pgdown":
		m.move(m.rows() / 2)
	case "ctrl+u", "pgup":
		m.move(-m.rows() / 2)
	case "?":
		m.mode = modeHelp
	case "r":
		return m, tea.Batch(loadCmd(m.path), m.say("reloading", false))
	case "a":
		if m.saving {
			return m, m.say("saving, one moment", true)
		}
		m.form = newForm(nil, m.pal)
		m.mode = modeForm
	case "E":
		line := 0
		if h != nil {
			line = h.Block.HeadLine + 1
		}
		return m, editorCmd(m.path, line)
	}
	if h == nil {
		if it := m.currentItem(); it != nil && it.kind == itemHeader {
			switch key {
			case "enter", "space", "left", "right", "h", "l":
				m.collapsed[it.group] = !m.collapsed[it.group]
				m.refilter()
			}
		}
		return m, nil
	}
	connectable := !h.Wildcard
	switch key {
	case "enter":
		if connectable {
			return m.startConnect(h.Alias)
		}
		return m, m.say("that stanza is defaults, not a host · E edits it", true)
	case "t", "s", "v", "f":
		if !connectable {
			return m, m.say("that stanza is defaults, not a host", true)
		}
		if m.slosh == nil {
			return m, m.say("not inside slosh: enter connects here", true)
		}
		how := map[string]string{"t": "tab", "s": "split", "v": "vsplit", "f": "float"}[key]
		return m, openCmd(m.slosh, how, h.Alias)
	case "o":
		if m.slosh == nil {
			return m, nil
		}
		panes := m.sessions[h.Alias]
		if len(panes) == 0 {
			return m, m.say("no open session for "+h.Alias+" · t opens one", true)
		}
		if err := m.slosh.Focus(panes[0]); err != nil {
			return m, m.say(shortErr(err), true)
		}
		return m, nil
	case "e":
		if h.ReadOnly {
			return m, m.say("read-only: included from "+h.Source, true)
		}
		if m.saving {
			return m, m.say("saving, one moment", true)
		}
		if h.Wildcard {
			return m, editorCmd(m.path, h.Block.HeadLine+1)
		}
		m.form = newForm(h, m.pal)
		m.mode = modeForm
	case "d":
		if h.ReadOnly {
			return m, m.say("read-only: included from "+h.Source, true)
		}
		if m.saving {
			return m, m.say("saving, one moment", true)
		}
		m.pending = h
		m.mode = modeConfirmDelete
	case "y":
		if connectable {
			return m, tea.Batch(tea.SetClipboard("ssh "+h.Alias), m.say("copied: ssh "+h.Alias, false))
		}
	case "i":
		if connectable {
			return m, terminfoCmd(h.Alias)
		}
	}
	return m, nil
}

// ---- helpers ----

func (m *Model) say(text string, warn bool) tea.Cmd {
	m.toast, m.toastWarn = text, warn
	m.toastUntil = time.Now().Add(3500 * time.Millisecond)
	return toastCmd(3600 * time.Millisecond)
}

// groupOf is the header a host sits under: its first tag, else untagged;
// wildcard stanzas keep their own section at the bottom.
func (m Model) groupOf(h *Host) string {
	switch {
	case h.Wildcard:
		return "defaults"
	case len(h.Tags) > 0:
		return h.Tags[0]
	}
	return "untagged"
}

func groupRank(g string) int {
	switch g {
	case "untagged":
		return 1
	case "defaults":
		return 2
	}
	return 0
}

func (m *Model) refilter() {
	q := m.filter.Value()
	m.items = m.items[:0]
	var hosts []int
	for i, h := range m.hosts {
		if q == "" || h.matches(q) {
			hosts = append(hosts, i)
		}
	}
	if !m.grouped {
		for _, i := range hosts {
			m.items = append(m.items, item{kind: itemHost, host: i})
		}
		m.clamp()
		return
	}
	groups := map[string][]int{}
	var order []string
	for _, i := range hosts {
		g := m.groupOf(m.hosts[i])
		if _, seen := groups[g]; !seen {
			order = append(order, g)
		}
		groups[g] = append(groups[g], i)
	}
	sort.SliceStable(order, func(a, b int) bool {
		ra, rb := groupRank(order[a]), groupRank(order[b])
		if ra != rb {
			return ra < rb
		}
		return order[a] < order[b]
	})
	for _, g := range order {
		m.items = append(m.items, item{kind: itemHeader, group: g, count: len(groups[g])})
		if m.collapsed[g] {
			continue
		}
		for _, i := range groups[g] {
			m.items = append(m.items, item{kind: itemHost, host: i, group: g})
		}
	}
	m.clamp()
	// A filter is for finding hosts: land on the first one rather than a header.
	if q != "" && m.current() == nil {
		for i, it := range m.items {
			if it.kind == itemHost {
				m.cursor = i
				m.clamp()
				break
			}
		}
	}
}

func (m *Model) move(d int) {
	m.cursor += d
	m.clamp()
}

func (m *Model) clamp() {
	if m.cursor >= len(m.items) {
		m.cursor = len(m.items) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	rows := m.rows()
	if rows <= 0 {
		return
	}
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+rows {
		m.offset = m.cursor - rows + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

// rows is how many host lines fit: the body minus frame, filter and spacer.
func (m Model) rows() int { return m.height - 1 - 2 - 2 }

func (m Model) currentItem() *item {
	if m.cursor < 0 || m.cursor >= len(m.items) {
		return nil
	}
	return &m.items[m.cursor]
}

// current is the selected host, nil on a group header.
func (m Model) current() *Host {
	if it := m.currentItem(); it != nil && it.kind == itemHost {
		return m.hosts[it.host]
	}
	return nil
}

func (m Model) byAlias(alias string) *Host {
	for _, h := range m.hosts {
		if h.Alias == alias && !h.Wildcard {
			return h
		}
	}
	return nil
}

func shortErr(err error) string {
	s := err.Error()
	if len(s) > 80 {
		s = s[:79] + "…"
	}
	return s
}
