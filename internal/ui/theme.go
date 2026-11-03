package ui

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// Palette is every colour the UI draws. Inside slosh it is lifted from the
// theme the session wears, so berth looks like one of slosh's own panes;
// elsewhere it is a neutral set picked for the terminal's background.
// Body text deliberately has no colour of its own: the terminal's default
// foreground keeps berth consistent with the panes around it.
type Palette struct {
	Name        string
	Accent      color.Color
	Border      color.Color
	BorderIdle  color.Color
	Title       color.Color
	TitleIdle   color.Color
	Muted       color.Color
	Hint        color.Color
	SelFg       color.Color
	SelBg       color.Color
	Ok          color.Color
	Warn        color.Color
	ModalBg     color.Color
	ModalBorder color.Color
	ModalTitle  color.Color
	ModalFg     color.Color
}

// PaletteFromSlosh maps slosh theme keys onto UI roles.
func PaletteFromSlosh(name string, t map[string]string) Palette {
	c := func(keys ...string) color.Color {
		for _, k := range keys {
			if v, ok := t[k]; ok {
				return lipgloss.Color(v)
			}
		}
		return nil
	}
	return Palette{
		Name:        "slosh:" + name,
		Accent:      c("frame_focus"),
		Border:      c("frame_focus"),
		BorderIdle:  c("frame_idle"),
		Title:       c("title"),
		TitleIdle:   c("title_idle"),
		Muted:       c("tab_idle", "status"),
		Hint:        c("hint"),
		SelFg:       c("finder_sel_fg"),
		SelBg:       c("finder_sel_bg"),
		Ok:          c("tab_status_busy", "frame_focus"),
		Warn:        c("search_cur_bg", "bell"),
		ModalBg:     c("modal_bg"),
		ModalBorder: c("modal_border"),
		ModalTitle:  c("modal_title"),
		ModalFg:     c("modal_fg"),
	}
}

// DefaultPalette is used outside slosh, by the terminal's background.
func DefaultPalette(dark bool) Palette {
	if dark {
		return Palette{
			Name:        "dark",
			Accent:      lipgloss.Color("#8aadf4"),
			Border:      lipgloss.Color("#8aadf4"),
			BorderIdle:  lipgloss.Color("#494d64"),
			Title:       lipgloss.Color("#cad3f5"),
			TitleIdle:   lipgloss.Color("#6e738d"),
			Muted:       lipgloss.Color("#8087a2"),
			Hint:        lipgloss.Color("#b8c0e0"),
			SelFg:       lipgloss.Color("#181926"),
			SelBg:       lipgloss.Color("#8aadf4"),
			Ok:          lipgloss.Color("#a6da95"),
			Warn:        lipgloss.Color("#eed49f"),
			ModalBg:     lipgloss.Color("#1e2030"),
			ModalBorder: lipgloss.Color("#8aadf4"),
			ModalTitle:  lipgloss.Color("#cad3f5"),
			ModalFg:     lipgloss.Color("#cad3f5"),
		}
	}
	return Palette{
		Name:        "light",
		Accent:      lipgloss.Color("#1e66f5"),
		Border:      lipgloss.Color("#1e66f5"),
		BorderIdle:  lipgloss.Color("#9ca0b0"),
		Title:       lipgloss.Color("#4c4f69"),
		TitleIdle:   lipgloss.Color("#8c8fa1"),
		Muted:       lipgloss.Color("#7c7f93"),
		Hint:        lipgloss.Color("#5c5f77"),
		SelFg:       lipgloss.Color("#eff1f5"),
		SelBg:       lipgloss.Color("#1e66f5"),
		Ok:          lipgloss.Color("#40a02b"),
		Warn:        lipgloss.Color("#df8e1d"),
		ModalBg:     lipgloss.Color("#e6e9ef"),
		ModalBorder: lipgloss.Color("#1e66f5"),
		ModalTitle:  lipgloss.Color("#4c4f69"),
		ModalFg:     lipgloss.Color("#4c4f69"),
	}
}

type styles struct {
	accent, muted, hint, title, titleIdle, ok, warn, sel, selMuted, modalTitle, modalFg lipgloss.Style
}

func newStyles(p Palette) styles {
	fg := func(c color.Color) lipgloss.Style {
		s := lipgloss.NewStyle()
		if c != nil {
			s = s.Foreground(c)
		}
		return s
	}
	sel := lipgloss.NewStyle()
	if p.SelBg != nil {
		sel = sel.Background(p.SelBg)
	}
	if p.SelFg != nil {
		sel = sel.Foreground(p.SelFg)
	}
	return styles{
		accent:     fg(p.Accent),
		muted:      fg(p.Muted),
		hint:       fg(p.Hint),
		title:      fg(p.Title).Bold(true),
		titleIdle:  fg(p.TitleIdle),
		ok:         fg(p.Ok),
		warn:       fg(p.Warn),
		sel:        sel.Bold(true),
		selMuted:   sel.Faint(true),
		modalTitle: fg(p.ModalTitle).Bold(true),
		modalFg:    fg(p.ModalFg),
	}
}
