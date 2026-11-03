// Package sshconf edits ~/.ssh/config in place: every line that is not
// touched by an edit is written back byte for byte, so comments, blank
// lines, indentation and directives this program knows nothing about
// survive a round trip.
package sshconf

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var (
	headerRe    = regexp.MustCompile(`^(\s*)(?i)(host|match)(\s+)(.+?)\s*$`)
	directiveRe = regexp.MustCompile(`^(\s*)([A-Za-z][A-Za-z0-9]*)(\s*=\s*|\s+)(.*?)\s*$`)
	commentRe   = regexp.MustCompile(`^\s*#`)
	tagsRe      = regexp.MustCompile(`^\s*#\s*[Tt]ags?\s*:\s*(.*?)\s*$`)
)

// File is one ssh_config file, plus the files it Includes.
type File struct {
	Path     string
	Lines    []string
	Blocks   []*Block
	Includes []*File
	// ReadOnly marks an included file; edits there are refused rather than
	// risking a file some other tool regenerates (lima, colima, ...).
	ReadOnly bool

	trailingNewline bool
	// What was on disk at load time, so Save can refuse to clobber edits
	// made elsewhere (another berth pane, an editor) since.
	loadedMod  time.Time
	loadedSize int64
}

// Validate, when set, is run on the fully written temp file before it
// replaces the config, so a line ssh refuses never lands on disk. main
// wires it to `ssh -G -F`.
var Validate func(tmpPath string) error

// ErrChanged is returned by Save when the file moved on under us.
var ErrChanged = errors.New("the file changed on disk since it was loaded")

// Max include nesting, the same cap ssh applies.
const maxIncludeDepth = 16

// Block is a Host or Match stanza: its header comments, the header line and
// the directives under it. Start..End is a half-open line range that
// excludes trailing blank lines.
type Block struct {
	File     *File
	Kind     string
	Patterns []string
	Start    int
	HeadLine int
	End      int
}

// Directive is one `Key value` line inside a block.
type Directive struct {
	Line  int
	Key   string
	Value string
}

// DefaultPath is ~/.ssh/config.
func DefaultPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ssh", "config")
}

// Load reads path and resolves its Include directives. A missing file is
// not an error: it loads as empty so the first host can be added to it.
func Load(path string) (*File, error) {
	return load(path, 0, map[string]bool{})
}

func load(path string, depth int, seen map[string]bool) (*File, error) {
	f := &File{Path: path, trailingNewline: true}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if st, err := os.Stat(path); err == nil {
		f.loadedMod, f.loadedSize = st.ModTime(), st.Size()
	}
	f.setContent(string(data))
	if key, err := filepath.EvalSymlinks(path); err == nil {
		seen[key] = true
	}
	if depth >= maxIncludeDepth {
		return f, nil
	}
	for _, inc := range f.includePaths() {
		key := inc
		if real, err := filepath.EvalSymlinks(inc); err == nil {
			key = real
		}
		if seen[key] {
			continue // a cycle, or the same file twice
		}
		sub, err := load(inc, depth+1, seen)
		if err != nil {
			continue
		}
		sub.ReadOnly = true
		f.Includes = append(f.Includes, sub)
	}
	return f, nil
}

// Parse is Load for text already in hand; Includes are not resolved.
func Parse(path, content string) *File {
	f := &File{Path: path}
	f.setContent(content)
	return f
}

func (f *File) setContent(content string) {
	f.trailingNewline = content == "" || strings.HasSuffix(content, "\n")
	content = strings.TrimSuffix(content, "\n")
	if content == "" {
		f.Lines = nil
	} else {
		f.Lines = strings.Split(content, "\n")
	}
	f.reparse()
}

// reparse rebuilds Blocks from Lines. Edits mutate Lines and call it, which
// is simpler and safer than shifting every block's indices by hand. When the
// stanza count is unchanged the existing *Block values are updated in place,
// so a pointer held across Set/Remove/Rename stays valid.
func (f *File) reparse() {
	var fresh []*Block
	for i, line := range f.Lines {
		m := headerRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		fresh = append(fresh, &Block{
			File:     f,
			Kind:     strings.ToLower(m[2]),
			Patterns: strings.Fields(m[4]),
			HeadLine: i,
		})
	}
	if len(fresh) == len(f.Blocks) {
		for i, b := range fresh {
			*f.Blocks[i] = *b
		}
	} else {
		f.Blocks = fresh
	}
	for i, b := range f.Blocks {
		// Header comments directly above the stanza belong to it.
		b.Start = b.HeadLine
		floor := 0
		if i > 0 {
			floor = f.Blocks[i-1].HeadLine + 1
		}
		for b.Start > floor && commentRe.MatchString(f.Lines[b.Start-1]) {
			b.Start--
		}
		// A comment run that opens the file describes the file, not its
		// first stanza (lima's generated config starts that way).
		if b.Start == 0 && b.HeadLine > 0 {
			b.Start = b.HeadLine
		}
	}
	for i, b := range f.Blocks {
		end := len(f.Lines)
		if i+1 < len(f.Blocks) {
			end = f.Blocks[i+1].Start
		}
		for end > b.HeadLine+1 && strings.TrimSpace(f.Lines[end-1]) == "" {
			end--
		}
		b.End = end
	}
}

func (f *File) includePaths() []string {
	var out []string
	// ssh resolves a relative Include in a user config against ~/.ssh,
	// whatever directory the config itself sits in.
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".ssh")
	for i, line := range f.Lines {
		d, ok := parseDirective(i, line)
		if !ok || !strings.EqualFold(d.Key, "Include") {
			continue
		}
		for _, p := range strings.Fields(d.Value) {
			p = strings.Trim(p, `"`)
			if strings.HasPrefix(p, "~/") {
				home, _ := os.UserHomeDir()
				p = filepath.Join(home, p[2:])
			} else if !filepath.IsAbs(p) {
				p = filepath.Join(dir, p)
			}
			matches, _ := filepath.Glob(p)
			out = append(out, matches...)
		}
	}
	return out
}

// Content is the file as it would be written.
func (f *File) Content() string {
	s := strings.Join(f.Lines, "\n")
	if f.trailingNewline && len(f.Lines) > 0 {
		s += "\n"
	}
	return s
}

// Save writes the file atomically with mode 0600: a sibling temp file is
// written, fsynced, checked by Validate, then renamed over the real file
// (the symlink target, when the config is a symlink into a dotfiles repo).
// It refuses when the file changed on disk since Load.
func (f *File) Save() error {
	if f.ReadOnly {
		return fmt.Errorf("%s is an included file; edit it with its own tool", f.Path)
	}
	target := f.Path
	if real, err := filepath.EvalSymlinks(f.Path); err == nil {
		target = real
	}
	if st, err := os.Stat(target); err == nil {
		if !f.loadedMod.IsZero() && (!st.ModTime().Equal(f.loadedMod) || st.Size() != f.loadedSize) {
			return ErrChanged
		}
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.berth-%d.tmp", target, os.Getpid())
	w, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	fail := func(err error) error {
		w.Close()
		os.Remove(tmp)
		return err
	}
	if _, err := w.WriteString(f.Content()); err != nil {
		return fail(err)
	}
	if err := w.Sync(); err != nil {
		return fail(err)
	}
	if err := w.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if Validate != nil {
		if err := Validate(tmp); err != nil {
			os.Remove(tmp)
			return err
		}
	}
	if err := os.Rename(tmp, target); err != nil {
		os.Remove(tmp)
		return err
	}
	if st, err := os.Stat(target); err == nil {
		f.loadedMod, f.loadedSize = st.ModTime(), st.Size()
	}
	return nil
}

// Hosts returns every Host block of this file and its includes, in file
// order, includes last.
func (f *File) Hosts() []*Block {
	var out []*Block
	for _, b := range f.Blocks {
		if b.Kind == "host" {
			out = append(out, b)
		}
	}
	for _, inc := range f.Includes {
		out = append(out, inc.Hosts()...)
	}
	return out
}

// Indent is the indentation most directives in the file use; ties go to
// whichever appears first, so the answer is stable.
func (f *File) Indent() string {
	counts := map[string]int{}
	var order []string
	for _, b := range f.Blocks {
		for _, d := range b.Directives() {
			ind := directiveRe.FindStringSubmatch(f.Lines[d.Line])[1]
			if ind == "" {
				continue
			}
			if counts[ind] == 0 {
				order = append(order, ind)
			}
			counts[ind]++
		}
	}
	best, n := "\t", 0
	for _, ind := range order {
		if counts[ind] > n {
			best, n = ind, counts[ind]
		}
	}
	return best
}

// AddHost appends a new `Host alias` stanza and returns it.
func (f *File) AddHost(alias string) *Block {
	if len(f.Lines) > 0 && strings.TrimSpace(f.Lines[len(f.Lines)-1]) != "" {
		f.Lines = append(f.Lines, "")
	}
	f.Lines = append(f.Lines, "Host "+alias)
	f.trailingNewline = true
	f.reparse()
	return f.Blocks[len(f.Blocks)-1]
}

// DeleteBlock removes a stanza and the blank lines that separated it.
func (f *File) DeleteBlock(b *Block) {
	end := b.End
	for end < len(f.Lines) && strings.TrimSpace(f.Lines[end]) == "" {
		end++
	}
	f.Lines = append(f.Lines[:b.Start], f.Lines[end:]...)
	// Don't leave two stanzas glued together, or a leading blank line.
	if b.Start > 0 && b.Start < len(f.Lines) && strings.TrimSpace(f.Lines[b.Start-1]) != "" {
		f.Lines = append(f.Lines[:b.Start], append([]string{""}, f.Lines[b.Start:]...)...)
	}
	for len(f.Lines) > 0 && strings.TrimSpace(f.Lines[len(f.Lines)-1]) == "" {
		f.Lines = f.Lines[:len(f.Lines)-1]
	}
	f.reparse()
}

func parseDirective(i int, line string) (Directive, bool) {
	if commentRe.MatchString(line) || strings.TrimSpace(line) == "" {
		return Directive{}, false
	}
	m := directiveRe.FindStringSubmatch(line)
	if m == nil {
		return Directive{}, false
	}
	return Directive{Line: i, Key: m[2], Value: m[4]}, true
}

// Directives lists the key/value lines of the block.
func (b *Block) Directives() []Directive {
	var out []Directive
	for i := b.HeadLine + 1; i < b.End; i++ {
		if d, ok := parseDirective(i, b.File.Lines[i]); ok {
			out = append(out, d)
		}
	}
	return out
}

// Get is the first value for key, which is the one ssh honours.
func (b *Block) Get(key string) (string, bool) {
	for _, d := range b.Directives() {
		if strings.EqualFold(d.Key, key) {
			return d.Value, true
		}
	}
	return "", false
}

// GetAll returns every value for key, for cumulative directives.
func (b *Block) GetAll(key string) []string {
	var out []string
	for _, d := range b.Directives() {
		if strings.EqualFold(d.Key, key) {
			out = append(out, d.Value)
		}
	}
	return out
}

// Set replaces the first `key` line's value, keeping its spelling,
// indentation and separator, or appends the directive to the stanza.
func (b *Block) Set(key, value string) {
	f := b.File
	for _, d := range b.Directives() {
		if strings.EqualFold(d.Key, key) {
			m := directiveRe.FindStringSubmatch(f.Lines[d.Line])
			f.Lines[d.Line] = m[1] + m[2] + m[3] + value
			return
		}
	}
	b.Append(key, value)
}

// Append adds a directive at the end of the stanza even if the key exists.
func (b *Block) Append(key, value string) {
	f := b.File
	at := b.HeadLine + 1
	if ds := b.Directives(); len(ds) > 0 {
		at = ds[len(ds)-1].Line + 1
	}
	line := b.indent() + key + " " + value
	f.Lines = append(f.Lines[:at], append([]string{line}, f.Lines[at:]...)...)
	f.reparse()
}

// Remove drops every `key` line whose value satisfies match (nil: all).
func (b *Block) Remove(key string, match func(value string) bool) {
	f := b.File
	var keep []string
	for i, line := range f.Lines {
		if i > b.HeadLine && i < b.End {
			if d, ok := parseDirective(i, line); ok && strings.EqualFold(d.Key, key) && (match == nil || match(d.Value)) {
				continue
			}
		}
		keep = append(keep, line)
	}
	f.Lines = keep
	f.reparse()
}

// Rename rewrites the header so alias replaces the stanza's current alias.
func (b *Block) Rename(alias string) {
	old := b.Alias()
	m := headerRe.FindStringSubmatch(b.File.Lines[b.HeadLine])
	var pats []string
	for _, p := range b.Patterns {
		if p == old {
			p = alias
		}
		pats = append(pats, p)
	}
	if old == "" {
		pats = append([]string{alias}, pats...)
	}
	b.File.Lines[b.HeadLine] = m[1] + m[2] + m[3] + strings.Join(pats, " ")
	b.File.reparse()
}

func (b *Block) indent() string {
	for _, d := range b.Directives() {
		return directiveRe.FindStringSubmatch(b.File.Lines[d.Line])[1]
	}
	return b.File.Indent()
}

// Alias is the first concrete pattern: what `ssh <alias>` connects to.
// Empty for stanzas made only of wildcards or negations (`Host *`).
func (b *Block) Alias() string {
	for _, p := range b.Patterns {
		if !strings.ContainsAny(p, "*?!") {
			return p
		}
	}
	return ""
}

// IsWildcard says the stanza is defaults, not a host you connect to.
func (b *Block) IsWildcard() bool { return b.Alias() == "" }

// HeaderComment is the comment text above the stanza, joined with spaces.
func (b *Block) HeaderComment() string {
	var parts []string
	for i := b.Start; i < b.HeadLine; i++ {
		t := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(b.File.Lines[i]), "#"))
		if t != "" && !tagsRe.MatchString(b.File.Lines[i]) {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, " ")
}

// Tags come from a `# tags: a, b` comment anywhere in the stanza.
func (b *Block) Tags() []string {
	for i := b.Start; i < b.End; i++ {
		if m := tagsRe.FindStringSubmatch(b.File.Lines[i]); m != nil {
			return splitTags(m[1])
		}
	}
	return nil
}

// SetTags writes, rewrites or removes the tags comment.
func (b *Block) SetTags(tags []string) {
	f := b.File
	for i := b.Start; i < b.End; i++ {
		if tagsRe.MatchString(f.Lines[i]) {
			if len(tags) == 0 {
				f.Lines = append(f.Lines[:i], f.Lines[i+1:]...)
			} else {
				m := regexp.MustCompile(`^(\s*)`).FindStringSubmatch(f.Lines[i])
				f.Lines[i] = m[1] + "# tags: " + strings.Join(tags, ", ")
			}
			f.reparse()
			return
		}
	}
	if len(tags) == 0 {
		return
	}
	at := b.HeadLine + 1
	line := b.indent() + "# tags: " + strings.Join(tags, ", ")
	f.Lines = append(f.Lines[:at], append([]string{line}, f.Lines[at:]...)...)
	f.reparse()
}

func splitTags(s string) []string {
	var out []string
	for _, t := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' }) {
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}
