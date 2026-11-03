package ui

import (
	"image/color"
	"math"
	"math/rand/v2"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
)

// The attach greeting, in the spirit of slosh's: the logo assembles under
// a colour effect for under a second, and any key cuts it short.

var logo = []string{
	"██████╗ ███████╗██████╗ ████████╗██╗  ██╗",
	"██╔══██╗██╔════╝██╔══██╗╚══██╔══╝██║  ██║",
	"██████╔╝█████╗  ██████╔╝   ██║   ███████║",
	"██╔══██╗██╔══╝  ██╔══██╗   ██║   ██╔══██║",
	"██████╔╝███████╗██║  ██║   ██║   ██║  ██║",
	"╚═════╝ ╚══════╝╚═╝  ╚═╝   ╚═╝   ╚═╝  ╚═╝",
}

const (
	splashDuration = 900 * time.Millisecond
	splashFrame    = 33 * time.Millisecond
	tagline        = "ssh, moored in ~/.ssh/config"
)

const (
	motionRain   = iota // columns fall into place
	motionDecode        // glyphs resolve out of static, left to right
	motionRise          // the logo surfaces from below
	motionCount
)

const (
	fxSweep   = iota // a highlight runs across a gradient
	fxBreathe        // the gradient brightens as it lands
	fxCount
)

type splash struct {
	start  time.Time
	motion int
	fx     int
	seed   uint64
}

func newSplash() *splash {
	return &splash{start: time.Now(), motion: rand.IntN(motionCount), fx: rand.IntN(fxCount), seed: rand.Uint64()}
}

// progress is 0 at start, 1 when the logo has landed, past 1 while held.
func (s *splash) progress() float64 {
	return float64(time.Since(s.start)) / float64(splashDuration)
}

func (s *splash) done() bool { return s.progress() >= 1.25 }

func easeOut(t float64) float64 {
	t = min(max(t, 0), 1)
	return 1 - math.Pow(1-t, 3)
}

// jitter is a stable pseudo-random in [0,1) for a cell.
func (s *splash) jitter(x, y int) float64 {
	h := s.seed ^ uint64(x)*0x9E3779B97F4A7C15 ^ uint64(y)*0xC2B2AE3D27D4EB4F
	h ^= h >> 29
	h *= 0xBF58476D1CE4E5B9
	h ^= h >> 32
	return float64(h%1000) / 1000
}

var static = []rune("░▒▓▚▞╬╪")

// renderSplash draws the greeting for the given progress, centred.
func (m Model) renderSplash(t float64) string {
	s := m.splash
	rows, cols := len(logo), lipgloss.Width(logo[0])
	grid := make([][]rune, rows)
	for y, line := range logo {
		grid[y] = []rune(line)
	}
	accent, title := m.pal.Accent, m.pal.Title
	if accent == nil {
		accent = lipgloss.Color("#8aadf4")
	}
	if title == nil {
		title = lipgloss.Color("#cad3f5")
	}
	ramp := lipgloss.Blend1D(cols, accent, title, accent)

	cell := func(x, y int) (rune, color.Color) {
		r := grid[y][x]
		if r == ' ' {
			return ' ', nil
		}
		c := ramp[x]
		switch m.splash.fx {
		case fxSweep:
			head := (easeOut(t)*1.3 - 0.15) * float64(cols)
			if d := head - float64(x); d >= 0 && d < 6 {
				c = lipgloss.Lighten(c, 0.45*(1-d/6))
			}
		case fxBreathe:
			c = lipgloss.Lighten(c, 0.35*(1-easeOut(t)))
		}
		return r, c
	}

	out := make([][]string, rows)
	for y := range out {
		out[y] = make([]string, cols)
		for x := range out[y] {
			out[y][x] = " "
		}
	}
	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			r, c := cell(x, y)
			if r == ' ' {
				continue
			}
			draw := func(rr rune, dy int, cc color.Color) {
				if yy := y + dy; yy >= 0 && yy < rows {
					out[yy][x] = lipgloss.NewStyle().Foreground(cc).Render(string(rr))
				}
			}
			switch s.motion {
			case motionRain:
				// Each column has its own landing time; before that it hangs above.
				local := easeOut((t - 0.35*s.jitter(x, 0)) / 0.65)
				dy := int(math.Round((1 - local) * float64(rows+2)))
				draw(r, -dy, c)
			case motionDecode:
				reveal := float64(x)/float64(cols)*0.7 + 0.3*s.jitter(x, y)
				if t >= reveal {
					draw(r, 0, c)
				} else if t >= reveal-0.25 {
					draw(static[int(s.jitter(x, y+7+int(t*20))*float64(len(static)))%len(static)], 0, m.pal.Muted)
				}
			case motionRise:
				local := easeOut(t / 0.8)
				dy := int(math.Round((1 - local) * float64(rows)))
				draw(r, dy, c)
			}
		}
	}
	lines := make([]string, rows)
	for y := range out {
		lines[y] = strings.Join(out[y], "")
	}
	tag := ""
	if t > 0.75 {
		shown := int(min(1, (t-0.75)/0.35) * float64(len(tagline)))
		tag = m.st.muted.Render(tagline[:shown])
	}
	block := strings.Join(lines, "\n") + "\n\n" + lipgloss.PlaceHorizontal(cols, lipgloss.Center, tag)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, block)
}
