package ui

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/axel-eck/berth/internal/probe"
	"github.com/axel-eck/berth/internal/sshconf"
)

type reach int

const (
	reachUnknown reach = iota
	reachProbing
	reachUp
	reachDown
	reachSkipped // behind a jump host: nothing to dial from here
)

// Host is one Host stanza plus what berth has learned about it.
type Host struct {
	Block    *sshconf.Block
	Alias    string
	Comment  string
	Tags     []string
	Wildcard bool
	ReadOnly bool
	Source   string
	Eff      probe.Effective
	Reach    reach
	Latency  time.Duration
	ReachErr string
}

func loadHosts(path string) (*sshconf.File, []*Host, error) {
	f, err := sshconf.Load(path)
	if err != nil {
		return nil, nil, err
	}
	var hosts, defaults []*Host
	for _, b := range f.Hosts() {
		h := &Host{
			Block:    b,
			Alias:    b.Alias(),
			Comment:  b.HeaderComment(),
			Tags:     b.Tags(),
			Wildcard: b.IsWildcard(),
			ReadOnly: b.File.ReadOnly,
			Source:   b.File.Path,
		}
		if h.Wildcard {
			h.Alias = strings.Join(b.Patterns, " ")
			defaults = append(defaults, h)
		} else {
			hosts = append(hosts, h)
		}
	}
	return f, append(hosts, defaults...), nil
}

func (h *Host) get(effKey, cfgKey string) string {
	if h.Eff != nil {
		return h.Eff.Get(effKey)
	}
	v, _ := h.Block.Get(cfgKey)
	return v
}

func (h *Host) hostname() string { return h.get("hostname", "HostName") }
func (h *Host) user() string     { return h.get("user", "User") }
func (h *Host) port() string {
	if p := h.get("port", "Port"); p != "" {
		return p
	}
	return "22"
}
func (h *Host) jump() string { return h.get("proxyjump", "ProxyJump") }

// proxied says the connection goes through a ProxyCommand.
func (h *Host) proxied() bool {
	if h.Eff == nil {
		return false
	}
	pc := h.Eff.Get("proxycommand")
	return pc != "" && pc != "none"
}

// target is user@host:port as the row shows it.
func (h *Host) target() string {
	if h.Wildcard {
		return "defaults"
	}
	t := h.hostname()
	if t == "" {
		t = h.Alias
	}
	if u := h.user(); u != "" {
		t = u + "@" + t
	}
	if p := h.port(); p != "22" {
		t += ":" + p
	}
	return t
}

// termOverride is the TERM a `SetEnv TERM=` line in the stanza forces,
// usually because the remote has no xterm-ghostty terminfo.
func (h *Host) termOverride() string {
	for _, v := range h.Block.GetAll("SetEnv") {
		for _, tok := range strings.Fields(v) {
			if t, ok := strings.CutPrefix(tok, "TERM="); ok {
				return t
			}
		}
	}
	return ""
}

var skRe = regexp.MustCompile(`_sk(\.pub)?$`)

// existingKeys filters ssh -G's identityfile list (which includes every
// default name) down to the files that are actually on disk.
func (h *Host) existingKeys() []string {
	if h.Eff == nil {
		return nil
	}
	home, _ := os.UserHomeDir()
	var out []string
	for _, f := range h.Eff["identityfile"] {
		p := f
		if strings.HasPrefix(p, "~/") {
			p = filepath.Join(home, p[2:])
		}
		if _, err := os.Stat(p); err == nil {
			out = append(out, f)
		}
	}
	return out
}

// authSummary says how ssh will authenticate, in the words the user thinks
// in: 1Password, YubiKey, a key file, or an agent.
func (h *Host) authSummary() string {
	if h.Eff == nil {
		return "…"
	}
	agent := h.Eff.Get("identityagent")
	only := h.Eff.Get("identitiesonly") == "yes"
	keys := h.existingKeys()
	names := make([]string, 0, len(keys))
	hasSK := false
	for _, k := range keys {
		names = append(names, filepath.Base(k))
		if skRe.MatchString(k) {
			hasSK = true
		}
	}
	switch {
	case strings.Contains(strings.ToLower(agent), "1password"):
		s := "1Password agent"
		if len(keys) > 0 && !only {
			s += " · " + strings.Join(names, ", ") + " as fallback"
		}
		return s
	case only && hasSK:
		return "YubiKey · " + strings.Join(names, ", ")
	case only && len(keys) > 0:
		return "key file · " + strings.Join(names, ", ")
	case only:
		return "IdentitiesOnly but no key on disk"
	case agent == "" || agent == "none":
		if os.Getenv("SSH_AUTH_SOCK") != "" && agent == "" {
			return "ssh-agent ($SSH_AUTH_SOCK)" + suffixKeys(names)
		}
		if len(keys) == 0 {
			return "no key on disk"
		}
		return "key file · " + strings.Join(names, ", ")
	default:
		return "agent " + filepath.Base(agent) + suffixKeys(names)
	}
}

func suffixKeys(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return " · " + strings.Join(names, ", ")
}

// forwards lists the port forwards the stanza resolves to.
func (h *Host) forwards() []string {
	if h.Eff == nil {
		return nil
	}
	var out []string
	for _, v := range h.Eff["localforward"] {
		out = append(out, "local  "+v)
	}
	for _, v := range h.Eff["remoteforward"] {
		out = append(out, "remote "+v)
	}
	for _, v := range h.Eff["dynamicforward"] {
		out = append(out, "socks  "+v)
	}
	return out
}

// matches is the filter: every space-separated word must appear in the
// alias, target, tags or comment.
func (h *Host) matches(query string) bool {
	var tags []string
	for _, t := range h.Tags {
		tags = append(tags, "#"+t)
	}
	hay := strings.ToLower(strings.Join([]string{h.Alias, h.target(), strings.Join(tags, " "), h.Comment}, " "))
	for _, w := range strings.Fields(strings.ToLower(query)) {
		if !strings.Contains(hay, w) {
			return false
		}
	}
	return true
}

// Auth presets the form offers. Each is a few directives on the stanza.
const (
	authInherit = "inherit"
	auth1P      = "1Password"
	authYubi    = "YubiKey"
	authKeyFile = "key file"
)

var authOptions = []string{authInherit, auth1P, authYubi, authKeyFile}

// onePasswordSocket is where the 1Password SSH agent listens.
func onePasswordSocket() string {
	home, _ := os.UserHomeDir()
	mac := filepath.Join(home, "Library/Group Containers/2BUA8C4S2C.com.1password/t/agent.sock")
	if _, err := os.Stat(mac); err == nil {
		return "~/Library/Group Containers/2BUA8C4S2C.com.1password/t/agent.sock"
	}
	return "~/.1password/agent.sock"
}

// securityKeys finds FIDO (YubiKey) keys in ~/.ssh.
func securityKeys() []string {
	home, _ := os.UserHomeDir()
	pubs, _ := filepath.Glob(filepath.Join(home, ".ssh", "*_sk.pub"))
	sort.Strings(pubs)
	var out []string
	for _, p := range pubs {
		out = append(out, "~/.ssh/"+strings.TrimSuffix(filepath.Base(p), ".pub"))
	}
	return out
}

// detectAuth reads the preset back out of a stanza's own directives.
func detectAuth(b *sshconf.Block) string {
	agent, _ := b.Get("IdentityAgent")
	only, _ := b.Get("IdentitiesOnly")
	files := b.GetAll("IdentityFile")
	switch {
	case strings.Contains(strings.ToLower(agent), "1password"):
		return auth1P
	case strings.EqualFold(only, "yes") && len(files) > 0 && skRe.MatchString(files[0]):
		return authYubi
	case strings.EqualFold(only, "yes"):
		return authKeyFile
	}
	return authInherit
}

// applyAuth writes a preset onto the stanza.
func applyAuth(b *sshconf.Block, preset, keyFile string) {
	b.Remove("IdentityAgent", nil)
	b.Remove("IdentitiesOnly", nil)
	b.Remove("IdentityFile", nil)
	if keyFile != "" {
		b.Set("IdentityFile", keyFile)
	}
	switch preset {
	case auth1P:
		b.Set("IdentityAgent", `"`+onePasswordSocket()+`"`)
	case authYubi:
		if keyFile == "" {
			if sk := securityKeys(); len(sk) > 0 {
				b.Set("IdentityFile", sk[0])
			}
		}
		b.Set("IdentitiesOnly", "yes")
		b.Set("IdentityAgent", "none")
	case authKeyFile:
		b.Set("IdentitiesOnly", "yes")
		b.Set("IdentityAgent", "none")
	}
}

// setTermOverride writes or clears the TERM token of the stanza's SetEnv,
// leaving any other variables on that line alone.
func setTermOverride(b *sshconf.Block, term string) {
	for _, d := range b.Directives() {
		if !strings.EqualFold(d.Key, "SetEnv") {
			continue
		}
		toks := strings.Fields(d.Value)
		var rest []string
		found := false
		for _, t := range toks {
			if strings.HasPrefix(t, "TERM=") {
				found = true
				continue
			}
			rest = append(rest, t)
		}
		if !found {
			continue
		}
		if term != "" {
			rest = append([]string{"TERM=" + term}, rest...)
		}
		if len(rest) == 0 {
			b.Remove("SetEnv", func(v string) bool { return v == d.Value })
		} else {
			b.Set("SetEnv", strings.Join(rest, " "))
		}
		return
	}
	if term != "" {
		b.Append("SetEnv", "TERM="+term)
	}
}
