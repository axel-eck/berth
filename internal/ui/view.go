package ui

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "berth"
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

func (m Model) listWidth() int {
	if m.width < 80 {
		return m.width
	}
	w := m.width * 2 / 5
	if w < 38 {
		w = 38
	}
	if w > 56 {
		w = 56
	}
	return w
}

func (m Model) render() string {
	if m.width == 0 || m.height < 6 {
		return ""
	}
	if m.mode == modeSplash && m.splash != nil {
		return m.renderSplash(m.splash.progress())
	}
	bodyH := m.height - 1
	if m.mode == modeConnect && m.conn != nil {
		w := min(m.width, 84)
		title := m.conn.alias
		var border color.Color
		if e := m.envOf(m.conn.alias); e != nil {
			title = e.Label + " · " + m.conn.alias
			border = e.Color
		}
		card := m.frameColored(title, m.connectLines(w-4), w, min(bodyH, 26), border)
		body := lipgloss.Place(m.width, bodyH, lipgloss.Center, lipgloss.Center, card)
		return body + "\n" + m.footer()
	}
	listW := m.listWidth()
	left := m.frame("berth", m.listLines(listW-4), listW, bodyH, m.mode != modeForm)
	body := left
	if detailW := m.width - listW; detailW >= 30 {
		var title string
		var lines []string
		focused := false
		switch {
		case m.mode == modeForm && m.form != nil:
			title, lines = m.formLines(detailW - 4)
			focused = true
		default:
			title, lines = m.detailLines(detailW - 4)
		}
		body = lipgloss.JoinHorizontal(lipgloss.Top, left, m.frame(title, lines, detailW, bodyH, focused))
	}
	out := body + "\n" + m.footer()
	switch m.mode {
	case modeConfirmDelete:
		out = m.overlay(out, m.modal("delete "+m.pending.Alias+"?", []string{
			"removes the stanza from " + tilde(m.pending.Source),
			"",
			m.st.accent.Render("y") + " delete    " + m.st.accent.Render("n") + " keep",
		}))
	case modeConfirmTerm:
		out = m.overlay(out, m.modal("xterm-ghostty installed on "+m.pending.Alias, []string{
			"drop its SetEnv TERM=" + m.pending.termOverride() + " override?",
			"Ghostty's own terminfo now works there.",
			"",
			m.st.accent.Render("y") + " drop it    " + m.st.accent.Render("n") + " keep",
		}))
	case modeHelp:
		out = m.overlay(out, m.modal("keys", helpLines(m.st, m.slosh != nil, tilde(m.path))))
	}
	return out
}

// frame draws a slosh-style rounded pane with the title in the top edge.
func (m Model) frame(title string, lines []string, w, h int, focused bool) string {
	bc := m.pal.BorderIdle
	if focused {
		bc = m.pal.Border
	}
	return m.frameColored(title, lines, w, h, bc)
}

// frameColored is frame with an explicit border colour (nil: idle).
func (m Model) frameColored(title string, lines []string, w, h int, bc color.Color) string {
	inner := w - 2
	tc := m.st.titleIdle
	if bc != nil && bc != m.pal.BorderIdle {
		tc = lipgloss.NewStyle().Foreground(bc).Bold(true)
	}
	border := lipgloss.NewStyle()
	if bc != nil {
		border = border.Foreground(bc)
	}
	t := " " + title + " "
	tw := lipgloss.Width(t)
	top := border.Render("╭─") + tc.Render(t) + border.Render(strings.Repeat("─", max(0, inner-1-tw))+"╮")
	rows := make([]string, 0, h)
	rows = append(rows, top)
	for i := 0; i < h-2; i++ {
		line := ""
		if i < len(lines) {
			line = lines[i]
		}
		rows = append(rows, border.Render("│")+" "+fit(line, inner-2)+" "+border.Render("│"))
	}
	rows = append(rows, border.Render("╰"+strings.Repeat("─", inner)+"╯"))
	return strings.Join(rows, "\n")
}

func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) > w {
		s = ansi.Truncate(s, w, "…")
	}
	return s + strings.Repeat(" ", max(0, w-lipgloss.Width(s)))
}

func (m Model) listLines(w int) []string {
	var lines []string
	if m.filterOn || m.filter.Value() != "" {
		m.filter.SetWidth(w - 2)
		lines = append(lines, m.st.accent.Render("/ ")+m.filter.View())
	} else {
		lines = append(lines, m.st.muted.Render("/ filter"))
	}
	lines = append(lines, "")
	if m.loadErr != "" {
		return append(lines, m.st.warn.Render(m.loadErr))
	}
	if len(m.items) == 0 {
		if len(m.hosts) == 0 {
			return append(lines, m.st.muted.Render("no hosts yet · a adds one"))
		}
		return append(lines, m.st.muted.Render("nothing matches"))
	}
	aliasW := 6
	for _, it := range m.items {
		if it.kind != itemHost {
			continue
		}
		if n := lipgloss.Width(m.hosts[it.host].Alias); n > aliasW && n <= 20 {
			aliasW = n
		}
	}
	rows := m.rows()
	for i := m.offset; i < len(m.items) && i < m.offset+rows; i++ {
		it := m.items[i]
		if it.kind == itemHeader {
			lines = append(lines, m.headerRow(it, i == m.cursor, w))
			continue
		}
		row := m.row(m.hosts[it.host], i == m.cursor, aliasW, w-indentW(m.grouped))
		if m.grouped && i != m.cursor {
			row = "  " + row
		} else if m.grouped {
			row = m.st.sel.Render("  ") + row
		}
		lines = append(lines, row)
	}
	return lines
}

func indentW(grouped bool) int {
	if grouped {
		return 2
	}
	return 0
}

func (m Model) headerRow(it item, selected bool, w int) string {
	mark := "▾"
	if m.collapsed[it.group] {
		mark = "▸"
	}
	label := it.group
	if groupRank(it.group) == 0 {
		label = "#" + it.group
	}
	plain := fmt.Sprintf("%s %s  %d", mark, label, it.count)
	if selected {
		return m.st.sel.Width(w).Render(plain)
	}
	head := m.st.accent
	if e := envOfTag(it.group); e != nil && groupRank(it.group) == 0 {
		head = lipgloss.NewStyle().Foreground(e.Color).Bold(true)
	}
	return head.Render(mark+" "+label) + m.st.muted.Render(fmt.Sprintf("  %d", it.count))
}

func (m Model) row(h *Host, selected bool, aliasW, w int) string {
	dot, dotStyle := " ", m.st.muted
	switch {
	case h.Wildcard:
		dot = "·"
	case h.Reach == reachUp:
		dot, dotStyle = "●", m.st.ok
	case h.Reach == reachDown:
		dot, dotStyle = "○", m.st.warn
	case h.Reach == reachProbing:
		dot = "◌"
	case h.Reach == reachSkipped:
		dot = "↗"
	}
	alias := fit(h.Alias, aliasW)
	var extras []string
	var envBadge string
	if e := h.env(); e != nil {
		envBadge = e.Label
	}
	if n := len(m.sessions[h.Alias]); n > 0 {
		extras = append(extras, fmt.Sprintf("⧉%d", n))
	}
	if h.ReadOnly {
		extras = append(extras, "⎘")
	}
	for i, t := range h.Tags {
		if m.grouped && i == 0 {
			continue // its header already says so
		}
		extras = append(extras, "#"+t)
	}
	extra := strings.Join(extras, " ")
	targetW := w - 2 - aliasW - 1
	if extra != "" {
		targetW -= lipgloss.Width(extra) + 1
	}
	if envBadge != "" {
		targetW -= lipgloss.Width(envBadge) + 1
	}
	target := fit(h.target(), max(targetW, 0))
	if selected {
		plain := dot + " " + alias + " "
		if envBadge != "" {
			plain += envBadge + " "
		}
		plain += target
		if extra != "" {
			plain += " " + extra
		}
		return m.st.sel.Width(w).Render(plain)
	}
	line := dotStyle.Render(dot) + " "
	if h.Wildcard {
		line += m.st.muted.Render(alias + " " + target)
	} else {
		line += alias + " "
		if e := h.env(); e != nil {
			line += lipgloss.NewStyle().Foreground(e.Color).Bold(true).Render(envBadge) + " "
		}
		line += m.st.muted.Render(target)
	}
	if extra != "" {
		line += " " + m.st.accent.Render(extra)
	}
	return line
}

func (m Model) detailLines(w int) (string, []string) {
	h := m.current()
	if h == nil {
		if it := m.currentItem(); it != nil && it.kind == itemHeader {
			return it.group, m.groupLines(*it)
		}
		return "berth", []string{m.st.muted.Render("select a host")}
	}
	label := func(s string) string { return m.st.muted.Render(fit(s, 9)) }
	var lines []string
	head := m.st.accent.Bold(true).Render(h.Alias)
	if h.ReadOnly {
		head += m.st.muted.Render("  read-only")
	}
	lines = append(lines, head)
	if !h.Wildcard {
		lines = append(lines, h.target())
	}
	if h.Comment != "" {
		lines = append(lines, m.st.muted.Render(h.Comment))
	}
	if len(h.Tags) > 0 {
		lines = append(lines, m.st.accent.Render("#"+strings.Join(h.Tags, " #")))
	}
	lines = append(lines, "")
	if !h.Wildcard {
		if e := h.env(); e != nil {
			note := "  marked on the pane frame and title while connected"
			if m.slosh == nil {
				note = "  the terminal background is tinted while connected"
			}
			lines = append(lines, label("env")+lipgloss.NewStyle().Foreground(e.Color).Bold(true).Render(e.Label)+m.st.muted.Render(note))
		}
		lines = append(lines, label("auth")+h.authSummary())
		if j := h.jump(); j != "" {
			lines = append(lines, label("jump")+"via "+j)
		}
		for i, f := range h.forwards() {
			l := "forward"
			if i > 0 {
				l = ""
			}
			lines = append(lines, label(l)+f)
		}
		if t := h.termOverride(); t != "" {
			lines = append(lines, label("term")+t+m.st.muted.Render("  override · i installs xterm-ghostty there"))
		}
		lines = append(lines, label("reach")+m.reachText(h))
		if m.slosh != nil {
			panes := m.sessions[h.Alias]
			switch len(panes) {
			case 0:
				lines = append(lines, label("sessions")+m.st.muted.Render("none open · t s v f open one"))
			default:
				var where []string
				for _, p := range panes {
					where = append(where, fmt.Sprintf("tab %d", p.Tab))
				}
				lines = append(lines, label("sessions")+fmt.Sprintf("%d open · %s", len(panes), strings.Join(where, ", "))+m.st.muted.Render("  o jumps"))
			}
		}
		lines = append(lines, "")
	}
	lines = append(lines, m.st.muted.Render(fmt.Sprintf("%s:%d", tilde(h.Source), h.Block.HeadLine+1)))
	for i := h.Block.Start; i < h.Block.End; i++ {
		raw := strings.ReplaceAll(h.Block.File.Lines[i], "\t", "    ")
		lines = append(lines, m.st.muted.Faint(true).Render(raw))
	}
	return h.Alias, lines
}

// groupLines is the detail pane for a header: the hosts under it.
func (m Model) groupLines(it item) []string {
	noun := "hosts"
	if it.count == 1 {
		noun = "host"
	}
	lines := []string{m.st.accent.Bold(true).Render(it.group), m.st.muted.Render(fmt.Sprintf("%d %s · space folds the group · # turns grouping off", it.count, noun)), ""}
	for _, h := range m.hosts {
		if m.groupOf(h) != it.group {
			continue
		}
		dot := m.st.muted.Render("·")
		switch h.Reach {
		case reachUp:
			dot = m.st.ok.Render("●")
		case reachDown:
			dot = m.st.warn.Render("○")
		}
		lines = append(lines, dot+" "+h.Alias+"  "+m.st.muted.Render(h.target()))
	}
	return lines
}

func (m Model) reachText(h *Host) string {
	switch h.Reach {
	case reachUp:
		return m.st.ok.Render("● up") + m.st.muted.Render(fmt.Sprintf("  %s to %s:%s", h.Latency.Round(100000), h.hostname(), h.port()))
	case reachDown:
		return m.st.warn.Render("○ unreachable") + m.st.muted.Render("  "+h.ReachErr)
	case reachProbing:
		return m.st.muted.Render("◌ probing " + h.hostname() + ":" + h.port())
	case reachSkipped:
		return m.st.muted.Render("↗ behind " + h.jump() + ", not probed")
	}
	return m.st.muted.Render("…")
}

func (m Model) formLines(w int) (string, []string) {
	f := m.form
	title := "new host"
	if f.host != nil {
		title = "edit " + f.host.Alias
	}
	var lines []string
	lines = append(lines, "")
	for i, fl := range f.fields {
		lab := fit(fl.label, 9)
		if i == f.cur {
			lab = m.st.accent.Render(lab)
		} else {
			lab = m.st.muted.Render(lab)
		}
		var val string
		if fl.kind == fieldSelect {
			var opts []string
			for j, o := range fl.options {
				switch {
				case j == fl.sel && i == f.cur:
					opts = append(opts, m.st.sel.Render(" "+o+" "))
				case j == fl.sel:
					opts = append(opts, m.st.accent.Render("["+o+"]"))
				default:
					opts = append(opts, m.st.muted.Render(" "+o+" "))
				}
			}
			val = strings.Join(opts, " ")
		} else {
			fl.input.SetWidth(w - 10)
			val = fl.input.View()
		}
		lines = append(lines, lab+" "+val)
	}
	lines = append(lines, "", m.st.muted.Render(f.fields[f.cur].help))
	if f.err != "" {
		lines = append(lines, "", m.st.warn.Render(f.err))
	}
	return title, lines
}

func (m Model) footer() string {
	var left string
	k := func(key, what string) string { return m.st.accent.Render(key) + m.st.muted.Render(" "+what) }
	switch m.mode {
	case modeConnect:
		left = k("esc", "cancel")
		if m.conn != nil && m.conn.failed != "" {
			left = k("enter", "retry") + "  " + k("esc", "back")
		}
	case modeForm:
		left = strings.Join([]string{k("enter", "next"), k("ctrl+s", "save"), k("←→", "choose"), k("esc", "cancel")}, "  ")
	default:
		hints := []string{k("enter", "connect")}
		if m.slosh != nil {
			hints = append(hints, k("t", "tab"), k("s", "split"), k("v", "vsplit"), k("f", "float"), k("o", "jump"))
		}
		hints = append(hints, k("a", "add"), k("e", "edit"), k("d", "del"), k("#", "groups"), k("i", "terminfo"), k("y", "yank"), k("/", "filter"))
		// Drop hints from the end until the row fits, keeping `? help` last.
		avail := m.width - lipgloss.Width(m.pal.Name) - 8
		for len(hints) > 1 && lipgloss.Width(strings.Join(hints, "  ")) > avail {
			hints = hints[:len(hints)-1]
		}
		left = strings.Join(append(hints, k("?", "help")), "  ")
	}
	right := m.st.muted.Render(m.pal.Name)
	if m.toast != "" {
		if m.toastWarn {
			right = m.st.warn.Render(m.toast)
		} else {
			right = m.st.ok.Render(m.toast)
		}
	}
	rw := lipgloss.Width(right)
	left = " " + ansi.Truncate(left, max(0, m.width-rw-3), "…")
	gap := max(1, m.width-lipgloss.Width(left)-rw)
	return fit(left+strings.Repeat(" ", gap)+right, m.width)
}

func (m Model) modal(title string, body []string) string {
	st := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(1, 2)
	if m.pal.ModalBorder != nil {
		st = st.BorderForeground(m.pal.ModalBorder)
	}
	if m.pal.ModalBg != nil {
		st = st.Background(m.pal.ModalBg).BorderBackground(m.pal.ModalBg)
	}
	if m.pal.ModalFg != nil {
		st = st.Foreground(m.pal.ModalFg)
	}
	// Border, padding and a margin of air on each side must fit the terminal.
	innerMax := max(10, m.width-2-4-4)
	wrapped := make([]string, 0, len(body))
	for _, l := range body {
		wrapped = append(wrapped, lipgloss.Wrap(l, innerMax, ""))
	}
	content := lipgloss.Wrap(m.st.modalTitle.Render(title), innerMax, "") + "\n\n" + strings.Join(wrapped, "\n")
	return st.Render(content)
}

// overlay centres box on top of bg, splicing it into each row.
func (m Model) overlay(bg, box string) string {
	bgLines := strings.Split(bg, "\n")
	boxLines := strings.Split(box, "\n")
	bw := min(lipgloss.Width(box), m.width)
	x := max(0, (m.width-bw)/2)
	y := max(0, (len(bgLines)-len(boxLines))/2)
	for i, bl := range boxLines {
		row := y + i
		if row >= len(bgLines) {
			break
		}
		line := fit(bgLines[row], m.width)
		bgLines[row] = ansi.Cut(line, 0, x) + fit(ansi.Truncate(bl, bw, ""), bw) + ansi.Cut(line, x+bw, m.width)
	}
	return strings.Join(bgLines, "\n")
}

func helpLines(st styles, inSlosh bool, cfg string) []string {
	k := func(key, what string) string { return st.accent.Render(fit(key, 10)) + what }
	lines := []string{
		k("enter", "connect here; the list comes back when ssh ends"),
	}
	if inSlosh {
		lines = append(lines,
			k("t", "open in a new slosh tab"),
			k("s / v", "open in a split below / beside this pane"),
			k("f", "open in a floating pane"),
			k("o", "jump to the host's open session"),
		)
	}
	lines = append(lines,
		k("a / e / d", "add / edit / delete a host in "+cfg),
		k("E", "open "+cfg+" in $VISUAL or $EDITOR at this host"),
		k("i", "install xterm-ghostty terminfo on the host"),
		k("y", "copy `ssh <alias>` to the clipboard"),
		k("/", "filter by alias, target, #tag or comment"),
		k("#", "group hosts under their first tag; space folds a group"),
		k("r", "reload the file and re-probe"),
		k("q", "quit"),
		"",
		st.muted.Render("● up  ○ unreachable  ↗ behind a jump  ⧉ open sessions  ⎘ included file"),
	)
	return lines
}

func tilde(p string) string {
	home, _ := os.UserHomeDir()
	if home != "" && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return filepath.Clean(p)
}
