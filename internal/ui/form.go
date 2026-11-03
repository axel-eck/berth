package ui

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/axel-eck/berth/internal/sshconf"
)

type fieldKind int

const (
	fieldText fieldKind = iota
	fieldSelect
)

type field struct {
	label   string
	kind    fieldKind
	input   textinput.Model
	options []string
	sel     int
	help    string
}

func (f *field) value() string {
	if f.kind == fieldSelect {
		return f.options[f.sel]
	}
	return strings.TrimSpace(f.input.Value())
}

// form adds or edits one stanza. It lives in the detail pane so the host
// list stays in view.
type form struct {
	host   *Host // nil when adding
	fields []*field
	cur    int
	err    string
}

const (
	fAlias = iota
	fHostName
	fUser
	fPort
	fJump
	fAuth
	fKeyFile
	fTerm
	fTags
)

func newForm(h *Host, pal Palette) *form {
	text := func(label, placeholder, value, help string) *field {
		ti := textinput.New()
		ti.Prompt = ""
		ti.Placeholder = placeholder
		ti.SetValue(value)
		ti.SetVirtualCursor(true)
		st := textinput.DefaultDarkStyles()
		st.Focused.Placeholder = newStyles(pal).muted
		st.Blurred.Placeholder = newStyles(pal).muted
		st.Cursor.Color = pal.Accent
		ti.SetStyles(st)
		return &field{label: label, input: ti, help: help}
	}
	sel := func(label string, options []string, current, help string) *field {
		f := &field{label: label, kind: fieldSelect, options: options, help: help}
		for i, o := range options {
			if o == current {
				f.sel = i
			}
		}
		return f
	}
	var alias, hostname, user, port, jump, auth, keyFile, term, tags string
	auth = authInherit
	if h != nil {
		b := h.Block
		alias = h.Alias
		hostname, _ = b.Get("HostName")
		user, _ = b.Get("User")
		port, _ = b.Get("Port")
		jump, _ = b.Get("ProxyJump")
		auth = detectAuth(b)
		if files := b.GetAll("IdentityFile"); len(files) > 0 {
			keyFile = files[0]
		}
		term = h.termOverride()
		tags = strings.Join(h.Tags, ", ")
	}
	termOpt := "inherit"
	if term != "" {
		termOpt = term
	}
	termOptions := []string{"inherit", "xterm-256color"}
	if term != "" && term != "xterm-256color" {
		termOptions = append(termOptions, term)
	}
	f := &form{host: h, fields: []*field{
		text("alias", "what you type after ssh", alias, "Host pattern: `ssh <alias>`"),
		text("hostname", "ip or dns name", hostname, "HostName"),
		text("user", "login (blank: your local user)", user, "User"),
		text("port", "22", port, "Port"),
		text("jump", "bastion alias, blank for direct", jump, "ProxyJump"),
		sel("auth", authOptions, auth, "←/→ to change. inherit keeps the Host * defaults"),
		text("key file", "~/.ssh/id_ed25519_sk (YubiKey picks it for you)", keyFile, "IdentityFile"),
		sel("TERM", termOptions, termOpt, "SetEnv TERM=… for hosts without xterm-ghostty terminfo"),
		text("tags", "homelab, prod", tags, "kept as a `# tags:` comment in the stanza"),
	}}
	f.fields[fAlias].input.Focus()
	return f
}

func (f *form) focus(i int) tea.Cmd {
	f.fields[f.cur].input.Blur()
	f.cur = (i + len(f.fields)) % len(f.fields)
	if f.fields[f.cur].kind == fieldText {
		return f.fields[f.cur].input.Focus()
	}
	return nil
}

// update returns (save, cancel, cmd).
func (f *form) update(msg tea.Msg) (bool, bool, tea.Cmd) {
	cur := f.fields[f.cur]
	if k, ok := msg.(tea.KeyPressMsg); ok {
		switch k.String() {
		case "esc":
			return false, true, nil
		case "ctrl+s":
			return true, false, nil
		case "tab", "down":
			return false, false, f.focus(f.cur + 1)
		case "shift+tab", "up":
			return false, false, f.focus(f.cur - 1)
		case "enter":
			if f.cur == len(f.fields)-1 {
				return true, false, nil
			}
			return false, false, f.focus(f.cur + 1)
		}
		if cur.kind == fieldSelect {
			switch k.String() {
			case "left", "h":
				cur.sel = (cur.sel - 1 + len(cur.options)) % len(cur.options)
			case "right", "l", "space":
				cur.sel = (cur.sel + 1) % len(cur.options)
			}
			return false, false, nil
		}
	}
	if cur.kind == fieldText {
		var cmd tea.Cmd
		cur.input, cmd = cur.input.Update(msg)
		return false, false, cmd
	}
	return false, false, nil
}

func (f *form) validate(hosts []*Host) error {
	alias := f.fields[fAlias].value()
	if alias == "" {
		return errors.New("alias is required")
	}
	if strings.ContainsAny(alias, " \t*?!") {
		return errors.New("alias cannot contain spaces or wildcards")
	}
	for _, h := range hosts {
		if h != f.host && !h.Wildcard && h.Alias == alias {
			return fmt.Errorf("%s already exists", alias)
		}
	}
	// ssh_config is line-oriented and berth writes values bare: anything
	// that would break the line, or read as another directive, is refused.
	for _, i := range []int{fHostName, fUser, fPort, fJump, fKeyFile} {
		v := f.fields[i].value()
		if v == "" {
			continue
		}
		if strings.ContainsAny(v, " \t\"'#") || strings.ContainsFunc(v, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			return fmt.Errorf("%s cannot contain spaces, quotes or # (ssh reads them as syntax)", f.fields[i].label)
		}
		if strings.HasPrefix(v, "-") {
			return fmt.Errorf("%s cannot start with -", f.fields[i].label)
		}
	}
	if p := f.fields[fPort].value(); p != "" {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return errors.New("port must be 1–65535")
		}
	}
	if f.fields[fAuth].value() == authKeyFile && f.fields[fKeyFile].value() == "" {
		return errors.New("key file auth needs a key file")
	}
	return nil
}

// apply writes the form onto the file. The caller saves.
func (f *form) apply(file *sshconf.File) *sshconf.Block {
	var b *sshconf.Block
	alias := f.fields[fAlias].value()
	if f.host == nil {
		b = file.AddHost(alias)
	} else {
		b = f.host.Block
		if b.Alias() != alias {
			b.Rename(alias)
		}
	}
	setOrRemove := func(key, val string) {
		if val == "" {
			b.Remove(key, nil)
		} else {
			b.Set(key, val)
		}
	}
	setOrRemove("HostName", f.fields[fHostName].value())
	setOrRemove("User", f.fields[fUser].value())
	setOrRemove("Port", f.fields[fPort].value())
	setOrRemove("ProxyJump", f.fields[fJump].value())
	applyAuth(b, f.fields[fAuth].value(), f.fields[fKeyFile].value())
	term := f.fields[fTerm].value()
	if term == "inherit" {
		term = ""
	}
	setTermOverride(b, term)
	var tags []string
	for _, t := range strings.FieldsFunc(f.fields[fTags].value(), func(r rune) bool { return r == ',' || r == ' ' }) {
		if t != "" {
			tags = append(tags, t)
		}
	}
	b.SetTags(tags)
	return b
}
