// Package slosh drives the session this program runs in over slosh's
// control socket, so a connection can become a tab, a split or a float
// without typing into anybody's shell.
package slosh

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// Client addresses one session. Nil when not running inside slosh.
type Client struct {
	Bin     string
	Session string
}

// Pane is the subset of a `panes` entry berth cares about.
type Pane struct {
	ID       int    `json:"id"`
	TabID    int    `json:"tab_id"`
	Tab      int    `json:"tab"`
	Title    string `json:"title"`
	Purpose  string `json:"purpose"`
	Status   string `json:"status"`
	Alive    bool   `json:"alive"`
	Focused  bool   `json:"focused"`
	Floating bool   `json:"floating"`
	X        int    `json:"x"`
	Y        int    `json:"y"`
	W        int    `json:"w"`
	H        int    `json:"h"`
	ContentW int    `json:"content_w"`
	ContentH int    `json:"content_h"`
}

// PaneByID looks one pane up.
func (c *Client) PaneByID(id int) (Pane, bool) {
	panes, err := c.Panes()
	if err != nil {
		return Pane{}, false
	}
	for _, p := range panes {
		if p.ID == id {
			return p, true
		}
	}
	return Pane{}, false
}

// SignPurpose marks the floating pane that carries a session's warning
// sign. It is not under the "berth:" prefix on purpose: Sessions must not
// count it as a session.
const SignPurpose = "berth-sign"

// OpenSign floats a small pane in the bottom-right corner of over, runs
// command in it (through the float's shell, the one way a float gets a
// command), and gives focus back to over. command receives the float's own
// pane id in place of %d, so the program can close its pane when done.
func (c *Client) OpenSign(over Pane, command string, w, h int) (int, error) {
	resp, err := c.Cmd(map[string]any{"cmd": "new-float"})
	if err != nil {
		return 0, err
	}
	idf, _ := resp["id"].(float64)
	id := int(idf)
	if id == 0 {
		return 0, errors.New("slosh new-float: no id")
	}
	c.Cmd(map[string]any{"cmd": "set-purpose", "target": "pane", "id": id, "purpose": SignPurpose})
	// One cell of air between the float and the pane's own frame.
	x := over.X + over.W - w - 2
	y := over.Y + over.H - h - 2
	if x < over.X {
		x = over.X
	}
	if y < over.Y {
		y = over.Y
	}
	c.Cmd(map[string]any{"cmd": "float", "id": id, "x": x, "y": y, "w": w, "h": h})
	c.Cmd(map[string]any{"cmd": "focus", "id": id})
	line := "exec " + fmt.Sprintf(command, id) + "\n"
	if _, err := c.Cmd(map[string]any{"cmd": "raw", "data": line}); err != nil {
		c.Close(id)
		return 0, err
	}
	c.Cmd(map[string]any{"cmd": "focus", "id": over.ID})
	return id, nil
}

// Detect returns a client when the process sits in a slosh pane.
func Detect() *Client {
	if os.Getenv("SLOSH") != "1" {
		return nil
	}
	bin := os.Getenv("SLOSH_BIN")
	if bin == "" {
		bin = "slosh"
	}
	sess := os.Getenv("SLOSH_SESSION")
	if sess == "" {
		sess = "main"
	}
	return &Client{Bin: bin, Session: sess}
}

// Cmd sends one control command and decodes the reply.
func (c *Client) Cmd(req map[string]any) (map[string]any, error) {
	body, _ := json.Marshal(req)
	out, err := exec.Command(c.Bin, "-s", c.Session, "cmd", string(body)).Output()
	if err != nil {
		return nil, fmt.Errorf("slosh %s: %w", req["cmd"], err)
	}
	var resp map[string]any
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("slosh %s: bad reply %q", req["cmd"], strings.TrimSpace(string(out)))
	}
	if ok, _ := resp["ok"].(bool); !ok {
		msg, _ := resp["error"].(string)
		return resp, fmt.Errorf("slosh %s: %s", req["cmd"], msg)
	}
	return resp, nil
}

// Panes lists every pane of the session.
func (c *Client) Panes() ([]Pane, error) {
	out, err := exec.Command(c.Bin, "-s", c.Session, "cmd", `{"cmd":"panes"}`).Output()
	if err != nil {
		return nil, err
	}
	var resp struct {
		OK    bool   `json:"ok"`
		Panes []Pane `json:"panes"`
	}
	if err := json.Unmarshal(out, &resp); err != nil || !resp.OK {
		return nil, errors.New("slosh panes: bad reply")
	}
	return resp.Panes, nil
}

// Here is the pane this program runs in: the focused one, since the user
// just pressed a key in it. slosh does not export a pane id to its children.
func (c *Client) Here() (Pane, error) {
	panes, err := c.Panes()
	if err != nil {
		return Pane{}, err
	}
	for _, p := range panes {
		if p.Focused {
			return p, nil
		}
	}
	return Pane{}, errors.New("no focused pane")
}

// OwnPane finds the pane this process was just started in: the focused
// pane when it carries purpose, else the only pane that does. 0 if unsure,
// because closing somebody else's pane is worse than leaving one behind.
func (c *Client) OwnPane(purpose string) int {
	panes, err := c.Panes()
	if err != nil {
		return 0
	}
	var matches []Pane
	for _, p := range panes {
		if p.Purpose == purpose {
			if p.Focused {
				return p.ID
			}
			matches = append(matches, p)
		}
	}
	if len(matches) == 1 {
		return matches[0].ID
	}
	return 0
}

// Close ends a pane; its tab goes with it when it was the last one.
func (c *Client) Close(id int) error {
	_, err := c.Cmd(map[string]any{"cmd": "close", "id": id})
	return err
}

// Purpose is the label a berth-spawned pane carries, so the host list can
// show live sessions and jump to them.
func Purpose(alias string) string { return "berth:" + alias }

func kdlQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// spawn starts command in a pane of a fresh tab, which is the one way the
// control API runs a command rather than a shell. Returns the new pane.
func (c *Client) spawn(name, command, purpose string) (Pane, error) {
	before := map[int]bool{}
	if panes, err := c.Panes(); err == nil {
		for _, p := range panes {
			before[p.ID] = true
		}
	}
	kdl := fmt.Sprintf("layout { tab name=%s { pane command=%s purpose=%s } }", kdlQuote(name), kdlQuote(command), kdlQuote(purpose))
	if _, err := c.Cmd(map[string]any{"cmd": "apply-layout", "kdl": kdl}); err != nil {
		return Pane{}, err
	}
	panes, err := c.Panes()
	if err != nil {
		return Pane{}, err
	}
	for _, p := range panes {
		if !before[p.ID] && p.Purpose == purpose {
			return p, nil
		}
	}
	return Pane{}, errors.New("slosh: spawned pane not found")
}

// OpenTab runs command in a new tab named after the host.
func (c *Client) OpenTab(alias, command string) error {
	_, err := c.spawn(alias, command, Purpose(alias))
	return err
}

// OpenSplit runs command beside the current pane; dir is "cols" or "rows".
func (c *Client) OpenSplit(alias, command, dir string) error {
	_, err := c.openHere(alias, command, dir)
	return err
}

// OpenFloat runs command in a floating pane over the current tab.
func (c *Client) OpenFloat(alias, command string) error {
	p, err := c.openHere(alias, command, "cols")
	if err != nil {
		return err
	}
	_, err = c.Cmd(map[string]any{"cmd": "float", "id": p.ID})
	return err
}

func (c *Client) openHere(alias, command, dir string) (Pane, error) {
	here, err := c.Here()
	if err != nil {
		return Pane{}, err
	}
	p, err := c.spawn(alias, command, Purpose(alias))
	if err != nil {
		return Pane{}, err
	}
	if _, err := c.Cmd(map[string]any{"cmd": "move-pane", "id": p.ID, "tab": here.TabID, "dir": dir}); err != nil {
		return Pane{}, err
	}
	c.Cmd(map[string]any{"cmd": "select-tab", "id": here.TabID})
	c.Cmd(map[string]any{"cmd": "focus", "id": p.ID})
	return p, nil
}

// Sessions maps host alias to the live panes berth opened for it.
func (c *Client) Sessions() map[string][]Pane {
	out := map[string][]Pane{}
	panes, err := c.Panes()
	if err != nil {
		return out
	}
	for _, p := range panes {
		if alias, ok := strings.CutPrefix(p.Purpose, "berth:"); ok && p.Alive {
			out[alias] = append(out[alias], p)
		}
	}
	return out
}

// Focus brings a pane to the front.
func (c *Client) Focus(p Pane) error {
	if _, err := c.Cmd(map[string]any{"cmd": "select-tab", "id": p.TabID}); err != nil {
		return err
	}
	_, err := c.Cmd(map[string]any{"cmd": "focus", "id": p.ID})
	return err
}

// SetPaneName overrides a pane's title (0: the focused pane); "" hands the
// title back to the program.
func (c *Client) SetPaneName(id int, name string) error {
	_, err := c.Cmd(map[string]any{"cmd": "set-name", "target": "pane", "id": id, "name": name})
	return err
}

// Notify puts a line in the session's status area.
func (c *Client) Notify(text string) {
	c.Cmd(map[string]any{"cmd": "notify", "text": text})
}

var (
	themeLine = regexp.MustCompile(`^\s*([a-z_]+)\s+"(#[0-9a-fA-F]{6})"`)
	startedRe = regexp.MustCompile(`^// Started from: (\S+)`)
)

func parseTheme(text string) (map[string]string, string) {
	theme := map[string]string{}
	from := ""
	for _, line := range strings.Split(text, "\n") {
		if m := themeLine.FindStringSubmatch(line); m != nil {
			theme[m[1]] = m[2]
		} else if m := startedRe.FindStringSubmatch(line); m != nil {
			from = m[1]
		}
	}
	return theme, from
}

// Theme is the palette the session is wearing right now: name and colours.
//
// `--dump-theme` resolves the configured theme with every derivation and
// override, but a theme picked live (prefix t) lives only in the session,
// so the session is asked which one it wears and that file is read on top.
func (c *Client) Theme() (string, map[string]string, error) {
	out, err := exec.Command(c.Bin, "--dump-theme").Output()
	if err != nil {
		return "", nil, err
	}
	theme, from := parseTheme(string(out))
	if len(theme) == 0 {
		return "", nil, errors.New("slosh --dump-theme: no colours")
	}
	name := from
	resp, err := c.Cmd(map[string]any{"cmd": "theme"})
	if err != nil {
		return name, theme, nil
	}
	live, _ := resp["theme"].(string)
	if live == "" || live == from {
		return name, theme, nil
	}
	dirs, _ := resp["dirs"].([]any)
	for _, d := range dirs {
		dir, _ := d.(string)
		data, err := os.ReadFile(dir + "/" + live + ".kdl")
		if err != nil {
			continue
		}
		override, _ := parseTheme(string(data))
		for k, v := range override {
			theme[k] = v
		}
		return live, theme, nil
	}
	return live, theme, nil
}

// ThemeName is what the session wears, without reading any file.
func (c *Client) ThemeName() string {
	resp, err := c.Cmd(map[string]any{"cmd": "theme"})
	if err != nil {
		return ""
	}
	name, _ := resp["theme"].(string)
	return name
}
