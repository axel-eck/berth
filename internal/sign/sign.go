// Package sign draws a warning sign as a rotating 3D wireframe prism in
// ASCII: the thing that floats over a production session so the pane can
// never be mistaken for staging.
package sign

import (
	"math"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type vec struct{ x, y, z float64 }

func (a vec) sub(b vec) vec     { return vec{a.x - b.x, a.y - b.y, a.z - b.z} }
func (a vec) cross(b vec) vec   { return vec{a.y*b.z - a.z*b.y, a.z*b.x - a.x*b.z, a.x*b.y - a.y*b.x} }
func (a vec) dot(b vec) float64 { return a.x*b.x + a.y*b.y + a.z*b.z }
func (a vec) norm() vec {
	l := math.Sqrt(a.dot(a))
	if l == 0 {
		return a
	}
	return vec{a.x / l, a.y / l, a.z / l}
}

// A triangular prism: the sign's face, with enough depth to read as a
// solid when it turns edge-on.
const depth = 0.42

var (
	top, left, right = vec{0, 1, 0}, vec{-1, -0.72, 0}, vec{1, -0.72, 0}
	// faces, counter-clockwise seen from outside so normals point out
	faces = [][]vec{
		{at(top, 1), at(left, 1), at(right, 1)},                  // front
		{at(top, -1), at(right, -1), at(left, -1)},               // back
		{at(top, 1), at(top, -1), at(left, -1), at(left, 1)},     // left side
		{at(top, -1), at(top, 1), at(right, 1), at(right, -1)},   // right side
		{at(left, 1), at(left, -1), at(right, -1), at(right, 1)}, // bottom
	}
	light  = vec{0.35, 0.6, 1}.norm()
	shades = []rune(" .:-=+*#%@")
)

func at(v vec, side float64) vec { return vec{v.x, v.y, side * depth / 2} }

// rotate spins around Y by theta and tilts around X by tilt.
func rotate(v vec, theta, tilt float64) vec {
	x := v.x*math.Cos(theta) + v.z*math.Sin(theta)
	z := -v.x*math.Sin(theta) + v.z*math.Cos(theta)
	y := v.y*math.Cos(tilt) - z*math.Sin(tilt)
	z = v.y*math.Sin(tilt) + z*math.Cos(tilt)
	return vec{x, y, z}
}

// Frame renders the sign at angle theta into a w×h grid of runes. bang
// marks the cells of the exclamation mark so a caller can colour them.
func Frame(theta float64, w, h int) (grid [][]rune, bang [][]bool) {
	grid = make([][]rune, h)
	bang = make([][]bool, h)
	for y := range grid {
		grid[y] = []rune(strings.Repeat(" ", w))
		bang[y] = make([]bool, w)
	}
	if w < 8 || h < 5 {
		return grid, bang
	}
	// A cell is about twice as tall as it is wide.
	scaleY := float64(h) / 2.5
	scaleX := scaleY * 2
	if scaleX > float64(w)/2.3 {
		scaleX = float64(w) / 2.3
		scaleY = scaleX / 2
	}
	project := func(v vec) (float64, float64) {
		p := 1 / (1 - v.z*0.28)
		return float64(w)/2 + v.x*p*scaleX, float64(h)/2 - v.y*p*scaleY
	}
	const tilt = 0.22
	for i, face := range faces {
		pts := make([]vec, len(face))
		for j, v := range face {
			pts[j] = rotate(v, theta, tilt)
		}
		n := pts[1].sub(pts[0]).cross(pts[2].sub(pts[0])).norm()
		if n.z <= 0.02 {
			continue // facing away
		}
		shade := shades[int(math.Min(float64(len(shades)-1), math.Max(0, n.dot(light))*float64(len(shades)-1)))]
		xs := make([]float64, len(pts))
		ys := make([]float64, len(pts))
		for j, p := range pts {
			xs[j], ys[j] = project(p)
		}
		fillPolygon(grid, xs, ys, shade)
		for j := range pts {
			k := (j + 1) % len(pts)
			line(grid, xs[j], ys[j], xs[k], ys[k], '#')
		}
		if i == 0 {
			// The exclamation mark rides the front face.
			cx := (xs[0] + xs[1] + xs[2]) / 3
			topY, botY := ys[0], (ys[1]+ys[2])/2
			start, end := topY+(botY-topY)*0.32, topY+(botY-topY)*0.72
			dot := topY + (botY-topY)*0.86
			col := int(math.Round(cx))
			for y := int(math.Round(start)); y <= int(math.Round(end)); y++ {
				put(grid, bang, col, y, '█')
			}
			put(grid, bang, col, int(math.Round(dot)), '▀')
		}
	}
	return grid, bang
}

func put(grid [][]rune, bang [][]bool, x, y int, r rune) {
	if y < 0 || y >= len(grid) || x < 0 || x >= len(grid[y]) {
		return
	}
	grid[y][x] = r
	bang[y][x] = true
}

func fillPolygon(grid [][]rune, xs, ys []float64, shade rune) {
	minY, maxY := math.Inf(1), math.Inf(-1)
	for _, y := range ys {
		minY, maxY = math.Min(minY, y), math.Max(maxY, y)
	}
	for row := int(math.Ceil(minY)); row <= int(math.Floor(maxY)); row++ {
		if row < 0 || row >= len(grid) {
			continue
		}
		y := float64(row)
		var crossings []float64
		for i := range xs {
			j := (i + 1) % len(xs)
			y0, y1 := ys[i], ys[j]
			if (y0 <= y && y1 > y) || (y1 <= y && y0 > y) {
				t := (y - y0) / (y1 - y0)
				crossings = append(crossings, xs[i]+t*(xs[j]-xs[i]))
			}
		}
		for i := 0; i+1 < len(crossings); i += 2 {
			a, b := crossings[i], crossings[i+1]
			if a > b {
				a, b = b, a
			}
			for x := int(math.Ceil(a)); x <= int(math.Floor(b)); x++ {
				if x >= 0 && x < len(grid[row]) {
					grid[row][x] = shade
				}
			}
		}
	}
}

func line(grid [][]rune, x0, y0, x1, y1 float64, r rune) {
	steps := math.Max(math.Abs(x1-x0), math.Abs(y1-y0)*2)
	n := int(math.Ceil(steps))
	if n == 0 {
		n = 1
	}
	for i := 0; i <= n; i++ {
		t := float64(i) / float64(n)
		x, y := int(math.Round(x0+(x1-x0)*t)), int(math.Round(y0+(y1-y0)*t))
		if y >= 0 && y < len(grid) && x >= 0 && x < len(grid[y]) {
			grid[y][x] = r
		}
	}
}

// Model animates the sign in a pane of its own.
type Model struct {
	Label  string
	Color  string
	theta  float64
	width  int
	height int
	// Watch is a function polled now and then; when it says false the
	// program quits (the session it decorated is gone).
	Watch func() bool
}

type tickMsg struct{}
type watchMsg struct{}

func (m Model) Init() tea.Cmd {
	return tea.Batch(tick(), watch())
}

func tick() tea.Cmd {
	return tea.Tick(70*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{} })
}

func watch() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return watchMsg{} })
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg.(type) {
	case tea.WindowSizeMsg:
		ws := msg.(tea.WindowSizeMsg)
		m.width, m.height = ws.Width, ws.Height
		return m, nil
	case tickMsg:
		m.theta += 0.11
		return m, tick()
	case watchMsg:
		if m.Watch != nil && !m.Watch() {
			return m, tea.Quit
		}
		return m, watch()
	case tea.KeyPressMsg:
		return m, tea.Quit
	}
	return m, nil
}

func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = m.Label
	return v
}

func (m Model) render() string {
	if m.width == 0 {
		return ""
	}
	h := m.height - 1 // the caption
	grid, bang := Frame(m.theta, m.width, max(1, h))
	color := lipgloss.Color(m.Color)
	body := lipgloss.NewStyle().Foreground(color)
	mark := lipgloss.NewStyle().Foreground(lipgloss.Color("#ffffff")).Bold(true)
	var b strings.Builder
	for y, row := range grid {
		var run strings.Builder
		for x, r := range row {
			if bang[y][x] {
				b.WriteString(body.Render(run.String()))
				run.Reset()
				b.WriteString(mark.Render(string(r)))
			} else {
				run.WriteRune(r)
			}
		}
		b.WriteString(body.Render(run.String()))
		b.WriteString("\n")
	}
	caption := lipgloss.NewStyle().Foreground(color).Bold(true).Render(m.Label)
	b.WriteString(lipgloss.PlaceHorizontal(m.width, lipgloss.Center, caption))
	return b.String()
}
