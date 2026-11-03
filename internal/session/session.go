// Package session runs ssh inside a pty berth owns, so the connection phase
// can be drawn by berth instead of ssh's own chatter, and hands the terminal
// over untouched once the remote shell is up.
//
// ssh is started with -v -E <file>: its log (debug steps, warnings,
// "Permission denied") goes to that file and only real questions (host key,
// PIN, passphrase) and remote output reach the pty. Both are parsed into
// Events until the session is interactive; from then on the pty is relayed.
package session

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/creack/pty"
	"github.com/muesli/cancelreader"
)

// Kind says what an Event is.
type Kind int

const (
	EvStep   Kind = iota // progress on one of the Steps
	EvNote               // an informational line: banner, warning, server version
	EvPrompt             // ssh is asking something; answer with Session.Answer
	EvReady              // the remote shell is up: hand the terminal over
	EvExit               // ssh exited before the session was ready
)

// Step is a phase of connecting.
type Step int

const (
	StepConnect Step = iota
	StepHostKey
	StepAuth
	StepSession
)

// Event is one thing the UI should show or ask.
type Event struct {
	Kind    Kind
	Step    Step
	Text    string
	Detail  string
	Done    bool // the step finished
	Attn    bool // the user has to do something physical (touch a key, approve in 1Password)
	Warn    bool // a note that deserves the warning colour
	Secret  bool // prompt input must be masked
	Choice  bool // prompt is yes/no
	Context []string
	Err     error
	Code    int
}

// SSH is the ssh binary to run. Resolved once by main from the launching
// shell's PATH and handed to spawned panes, because a pane slosh creates
// has a bare PATH where macOS's own ssh (built without FIDO support, so
// without YubiKeys) comes first.
var SSH = "ssh"

// ConfigFile mirrors probe.ConfigFile: an explicit config to connect with.
var ConfigFile string

// Args is the -F prefix when a config file was chosen.
func Args() []string {
	if ConfigFile != "" {
		return []string{"-F", ConfigFile}
	}
	return nil
}

// Session is one ssh process from start to exit.
type Session struct {
	Alias   string
	Events  chan Event
	Started time.Time

	cmd     *exec.Cmd
	pty     *os.File
	logPath string

	mu     sync.Mutex
	ready  bool
	authed bool // past authentication: unknown pty output is the shell
	// Prompts are believed only when ssh's own log says it is asking: a
	// remote banner can print "Enter PIN ...: " too, and a masked field
	// would lend it authority it has not earned.
	secretArmed  bool
	hostKeyAsked bool
	logRemoved   bool
	swallow      string    // the echo of an answer just typed, not session output
	out          io.Writer // set by the proxy: pty output goes here from then on
	pending      []byte    // pty output read before the proxy attached
	partial      string    // an incomplete tty line, which may be a prompt
	context      []string  // tty lines leading up to a prompt
	lastErr      string

	done     chan struct{}
	exitCode int
	exitErr  error
}

// Start launches ssh for alias with a pty of the given size.
func Start(alias string, cols, rows int) (*Session, error) {
	logFile, err := os.CreateTemp("", "berth-ssh-*.log")
	if err != nil {
		return nil, err
	}
	logFile.Close()
	args := append([]string{"-v", "-E", logFile.Name()}, Args()...)
	cmd := exec.Command(SSH, append(args, "--", alias)...)
	cmd.Env = os.Environ()
	ws := &pty.Winsize{Cols: uint16(max(cols, 20)), Rows: uint16(max(rows, 5))}
	ptmx, err := pty.StartWithSize(cmd, ws)
	if err != nil {
		os.Remove(logFile.Name())
		return nil, err
	}
	s := &Session{
		Alias:   alias,
		Events:  make(chan Event, 64),
		Started: time.Now(),
		cmd:     cmd,
		pty:     ptmx,
		logPath: logFile.Name(),
		done:    make(chan struct{}),
	}
	go s.readPty()
	go s.tailLog()
	return s, nil
}

// Answer types a reply to the current prompt.
func (s *Session) Answer(text string) {
	s.mu.Lock()
	s.partial, s.context = "", nil
	s.secretArmed, s.hostKeyAsked = false, false
	s.swallow = text
	s.mu.Unlock()
	s.pty.Write([]byte(text + "\n"))
}

// Kill ends ssh.
func (s *Session) Kill() {
	if s.cmd.Process != nil {
		s.cmd.Process.Kill()
	}
}

// ExitCode is ssh's exit status once it has exited.
func (s *Session) ExitCode() int { return s.exitCode }

// Done closes when ssh has exited.
func (s *Session) Done() <-chan struct{} { return s.done }

// Elapsed is how long the session has been running.
func (s *Session) Elapsed() time.Duration { return time.Since(s.Started).Round(time.Second) }

func (s *Session) emit(e Event) {
	select {
	case s.Events <- e:
	case <-time.After(time.Second):
	}
}

// readPty is the only reader of the pty for the session's whole life: it
// parses until ready, then relays to whatever the proxy attached.
func (s *Session) readPty() {
	buf := make([]byte, 32*1024)
	for {
		n, err := s.pty.Read(buf)
		if n > 0 {
			s.mu.Lock()
			switch {
			case s.out != nil:
				s.out.Write(buf[:n])
			case s.ready:
				s.pending = append(s.pending, buf[:n]...)
			default:
				s.mu.Unlock()
				s.parseTTY(buf[:n])
				s.mu.Lock()
			}
			s.mu.Unlock()
		}
		if err != nil {
			break
		}
	}
	err := s.cmd.Wait()
	s.pty.Close()
	s.exitErr = err
	if ee := (&exec.ExitError{}); errors.As(err, &ee) {
		s.exitCode = ee.ExitCode()
	}
	time.Sleep(60 * time.Millisecond) // let the log tail catch the last lines
	s.mu.Lock()
	wasReady := s.ready
	lastErr := s.lastErr
	s.mu.Unlock()
	if !wasReady {
		msg := lastErr
		if msg == "" {
			msg = fmt.Sprintf("ssh exited (code %d)", s.exitCode)
		}
		s.emit(Event{Kind: EvExit, Code: s.exitCode, Err: errors.New(msg)})
	}
	close(s.done)
	os.Remove(s.logPath)
}

var (
	rePIN       = regexp.MustCompile(`Enter PIN for (\S+) key (.*?): ?$`)
	rePassword  = regexp.MustCompile(`(.*)'s password: ?$`)
	rePassphr   = regexp.MustCompile(`Enter passphrase for key '(.*)': ?$`)
	reHostAsk   = regexp.MustCompile(`Are you sure you want to continue connecting \(yes/no(?:/\[fingerprint\])?\)\? ?$`)
	rePresence  = regexp.MustCompile(`Confirm user presence for key (\S+) (\S+)`)
	reConfirmed = regexp.MustCompile(`User presence confirmed`)
)

func (s *Session) parseTTY(b []byte) {
	s.mu.Lock()
	text := s.partial + string(b)
	s.mu.Unlock()
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	rest := lines[len(lines)-1]
	for _, line := range lines[:len(lines)-1] {
		s.ttyLine(line)
	}
	s.mu.Lock()
	s.partial = rest
	s.mu.Unlock()
	s.ttyPrompt(rest)
}

func (s *Session) ttyLine(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	switch {
	case rePresence.MatchString(line):
		m := rePresence.FindStringSubmatch(line)
		s.emit(Event{Kind: EvStep, Step: StepAuth, Text: "touch your YubiKey", Detail: m[1] + " " + m[2], Attn: true})
	case reConfirmed.MatchString(line):
		s.emit(Event{Kind: EvStep, Step: StepAuth, Text: "presence confirmed"})
	case strings.Contains(line, "Permission denied, please try again"):
		s.emit(Event{Kind: EvNote, Text: "wrong answer, ssh asks again", Warn: true})
	default:
		s.mu.Lock()
		if s.swallow != "" && line == s.swallow {
			// The tty echoing what Answer typed.
			s.swallow = ""
			s.mu.Unlock()
			return
		}
		// Not one of ssh's own lines, so it is a banner or the first remote
		// output: keep it for the session screen as well as the card.
		s.pending = append(s.pending, []byte(line+"\r\n")...)
		s.context = append(s.context, line)
		if len(s.context) > 12 {
			s.context = s.context[1:]
		}
		authed, ready := s.authed, s.ready
		s.mu.Unlock()
		if authed && !ready {
			s.markReady()
			return
		}
		s.classify(line)
	}
}

// markReady flips to the handover phase once.
func (s *Session) markReady() {
	s.mu.Lock()
	already := s.ready
	s.ready = true
	s.mu.Unlock()
	if already {
		return
	}
	s.emit(Event{Kind: EvStep, Step: StepSession, Text: "shell ready", Done: true})
	s.emit(Event{Kind: EvReady})
}

// ttyPrompt recognises a question ssh left on the line without a newline.
func (s *Session) ttyPrompt(rest string) { s.promptAttempt(rest, 0) }

// promptAttempt believes a prompt only once the log has shown ssh is at
// that point. The pty and the log are read independently, so the question
// can land a few milliseconds before its log line: wait a little and look
// again, as long as the line is still sitting there unanswered. Only when
// the log never backs it up is it shown as text the remote printed.
func (s *Session) promptAttempt(rest string, tries int) {
	rest = strings.TrimRight(rest, " ")
	if rest == "" {
		return
	}
	s.mu.Lock()
	ctx := append([]string(nil), s.context...)
	armed, asked, authed := s.secretArmed, s.hostKeyAsked, s.authed
	s.mu.Unlock()
	later := func() bool {
		if authed || tries >= 15 {
			return false
		}
		time.AfterFunc(60*time.Millisecond, func() {
			s.mu.Lock()
			same := strings.TrimRight(s.partial, " ") == rest
			s.mu.Unlock()
			if same {
				s.promptAttempt(rest, tries+1)
			}
		})
		return true
	}
	note := func() { s.emit(Event{Kind: EvNote, Text: strings.TrimSpace(rest), Warn: true}) }
	secret := func(text, detail string, warn bool) {
		if authed || !armed {
			if !later() {
				note()
			}
			return
		}
		s.forgetContext()
		s.emit(Event{Kind: EvPrompt, Step: StepAuth, Text: text, Detail: detail, Secret: true, Warn: warn, Context: ctx})
	}
	switch {
	case rePIN.MatchString(rest):
		m := rePIN.FindStringSubmatch(rest)
		secret("PIN", m[1]+" "+filepath.Base(m[2]), false)
	case rePassphr.MatchString(rest):
		m := rePassphr.FindStringSubmatch(rest)
		secret("passphrase", filepath.Base(m[1]), false)
	case rePassword.MatchString(rest):
		m := rePassword.FindStringSubmatch(rest)
		secret("password", m[1], true)
	case reHostAsk.MatchString(rest):
		if !asked || authed {
			if !later() {
				note()
			}
			return
		}
		s.forgetContext()
		s.emit(Event{Kind: EvPrompt, Step: StepHostKey, Text: "trust this host key?", Choice: true, Context: ctx})
	}
}

// forgetContext drops the lines that led up to a prompt from the session
// replay: the card shows them with the question, the shell need not.
func (s *Session) forgetContext() {
	s.mu.Lock()
	s.pending = nil
	s.mu.Unlock()
}

var (
	reConnecting  = regexp.MustCompile(`^debug1: Connecting to (\S+) \[(\S+)\] port (\d+)\.`)
	reEstablished = regexp.MustCompile(`^debug1: Connection established\.`)
	reRemoteVer   = regexp.MustCompile(`^debug1: Remote protocol version \S+, remote software version (.*)`)
	reHostKey     = regexp.MustCompile(`^debug1: Server host key: (\S+) (\S+)`)
	reHostKnown   = regexp.MustCompile(`^debug1: Host '.*' is known and matches the (\S+) host key\.`)
	reHostAdded   = regexp.MustCompile(`^Warning: Permanently added '(.*)' \((\S+)\) to the list of known hosts\.`)
	reOffering    = regexp.MustCompile(`^debug1: Offering public key: (\S+) (\S+) (\S+)(.*)`)
	reAccepts     = regexp.MustCompile(`^debug1: Server accepts key: (\S+) (\S+) (\S+)(.*)`)
	reAuthOK      = regexp.MustCompile(`^debug1: Authentication succeeded \((\S+)\)\.`)
	reAuthedTo    = regexp.MustCompile(`^Authenticated to (\S+) .*using "(\S+)"`)
	reInteractive = regexp.MustCompile(`^debug1: Entering interactive session\.`)
	reJump        = regexp.MustCompile(`^debug1: Setting implicit ProxyCommand from ProxyJump: (.*)`)
	reMuxTry      = regexp.MustCompile(`^debug1: auto-mux: Trying existing master`)
	reMuxSession  = regexp.MustCompile(`^debug1: mux_client_request_session: master session id: \d+`)
	reNextMethod  = regexp.MustCompile(`^debug1: Next authentication method: (password|keyboard-interactive)`)
	reTryingKey   = regexp.MustCompile(`^debug1: Trying private key: `)
	rePQ          = regexp.MustCompile(`^\*\* WARNING: connection is not using a post-quantum`)
	reDebug       = regexp.MustCompile(`^debug\d: `)
)

func (s *Session) tailLog() {
	var f *os.File
	for f == nil {
		var err error
		if f, err = os.Open(s.logPath); err != nil {
			select {
			case <-s.done:
				return
			case <-time.After(20 * time.Millisecond):
			}
		}
	}
	defer f.Close()
	r := bufio.NewReader(f)
	var carry []byte
	for {
		chunk, err := r.ReadBytes('\n')
		if len(chunk) > 0 {
			carry = append(carry, chunk...)
			if bytes.HasSuffix(carry, []byte("\n")) {
				s.logLine(strings.TrimRight(string(carry), "\r\n"))
				carry = nil
			}
		}
		if err != nil {
			select {
			case <-s.done:
				return
			case <-time.After(25 * time.Millisecond):
			}
		}
	}
}

func (s *Session) logLine(line string) {
	s.mu.Lock()
	if !s.logRemoved {
		// ssh has the log open (it just wrote to it); unlink it now so
		// nothing is left in $TMPDIR however the process ends.
		s.logRemoved = true
		os.Remove(s.logPath)
	}
	s.mu.Unlock()
	switch {
	case reInteractive.MatchString(line):
		s.markReady()
	case reMuxTry.MatchString(line):
		s.emit(Event{Kind: EvStep, Step: StepConnect, Text: "reusing the open connection", Done: true})
		s.emit(Event{Kind: EvStep, Step: StepHostKey, Text: "host key verified earlier", Done: true})
	case reMuxSession.MatchString(line):
		// A multiplexed session skips connect and auth entirely.
		s.mu.Lock()
		s.authed = true
		s.mu.Unlock()
		s.emit(Event{Kind: EvStep, Step: StepAuth, Text: "authenticated earlier", Done: true})
		s.markReady()
	case reConnecting.MatchString(line):
		m := reConnecting.FindStringSubmatch(line)
		s.emit(Event{Kind: EvStep, Step: StepConnect, Text: "connecting to " + m[2] + ":" + m[3]})
	case reJump.MatchString(line):
		// The implicit ProxyCommand ends with the jump host's name.
		fields := strings.Fields(reJump.FindStringSubmatch(line)[1])
		jump := ""
		if len(fields) > 0 {
			jump = fields[len(fields)-1]
		}
		s.emit(Event{Kind: EvStep, Step: StepConnect, Text: "through " + jump})
	case reEstablished.MatchString(line):
		s.emit(Event{Kind: EvStep, Step: StepConnect, Text: "connected", Done: true})
	case reRemoteVer.MatchString(line):
		s.emit(Event{Kind: EvNote, Text: "server " + reRemoteVer.FindStringSubmatch(line)[1]})
	case reHostKey.MatchString(line):
		m := reHostKey.FindStringSubmatch(line)
		s.mu.Lock()
		s.hostKeyAsked = true
		s.mu.Unlock()
		s.emit(Event{Kind: EvStep, Step: StepHostKey, Text: "host key " + keyType(m[1]), Detail: m[2]})
	case reHostKnown.MatchString(line):
		s.mu.Lock()
		s.hostKeyAsked = false
		s.mu.Unlock()
		s.emit(Event{Kind: EvStep, Step: StepHostKey, Text: "host key known", Done: true})
	case reHostAdded.MatchString(line):
		s.mu.Lock()
		s.hostKeyAsked = false
		s.mu.Unlock()
		s.emit(Event{Kind: EvStep, Step: StepHostKey, Text: "host key added to known_hosts", Done: true})
	case reNextMethod.MatchString(line), reTryingKey.MatchString(line):
		s.mu.Lock()
		s.secretArmed = true
		s.mu.Unlock()
	case reOffering.MatchString(line):
		m := reOffering.FindStringSubmatch(line)
		s.emit(Event{Kind: EvStep, Step: StepAuth, Text: "offering " + keyName(m[1], m[4]), Detail: m[2] + " " + m[3]})
	case reAccepts.MatchString(line):
		m := reAccepts.FindStringSubmatch(line)
		s.mu.Lock()
		s.secretArmed = true // a PIN or passphrase may be asked to sign
		s.mu.Unlock()
		text, attn := "key accepted, signing", false
		switch {
		case strings.Contains(m[4], "agent") || !strings.HasPrefix(m[1], "/"):
			text, attn = "approve in your agent", true // the UI names it
		case strings.Contains(m[4], "authenticator"):
			text, attn = "touch your YubiKey", true
		}
		s.emit(Event{Kind: EvStep, Step: StepAuth, Text: text, Detail: m[2] + " " + m[3], Attn: attn})
	case reAuthOK.MatchString(line):
		s.mu.Lock()
		s.authed = true
		s.mu.Unlock()
		s.emit(Event{Kind: EvStep, Step: StepAuth, Text: "authenticated (" + reAuthOK.FindStringSubmatch(line)[1] + ")", Done: true})
	case reAuthedTo.MatchString(line):
		s.mu.Lock()
		s.authed = true
		s.mu.Unlock()
		s.emit(Event{Kind: EvStep, Step: StepAuth, Text: "authenticated with " + reAuthedTo.FindStringSubmatch(line)[2], Done: true})
	case rePQ.MatchString(line):
		s.emit(Event{Kind: EvNote, Text: "no post-quantum key exchange: the server should be upgraded", Warn: true})
	case reDebug.MatchString(line):
		// the rest of -v is noise
	default:
		s.classify(line)
	}
}

// classify handles lines that are neither steps nor prompts: errors ssh
// reports, and anything else as a note (banners, MOTD before the shell).
func (s *Session) classify(line string) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "** ") {
		return
	}
	isErr := false
	for _, p := range []string{
		"Permission denied", "Connection refused", "Connection timed out", "Could not resolve hostname",
		"No route to host", "Connection closed by", "Connection reset", "Host key verification failed",
		"kex_exchange_identification", "Operation timed out", "Network is unreachable", "Bad configuration",
		"REMOTE HOST IDENTIFICATION HAS CHANGED", "Too many authentication failures", "Broken pipe",
	} {
		if strings.Contains(line, p) {
			isErr = true
			break
		}
	}
	if strings.HasPrefix(line, "@@@") {
		return
	}
	if isErr {
		msg := strings.TrimPrefix(line, "ssh: ")
		s.mu.Lock()
		s.lastErr = msg
		s.mu.Unlock()
		s.emit(Event{Kind: EvNote, Text: msg, Warn: true})
		return
	}
	s.emit(Event{Kind: EvNote, Text: line})
}

func keyType(alg string) string {
	alg = strings.TrimPrefix(alg, "ssh-")
	alg = strings.TrimSuffix(alg, "@openssh.com")
	return strings.ToUpper(alg)
}

func keyName(path, extra string) string {
	name := filepath.Base(path)
	if !strings.HasPrefix(path, "/") && !strings.HasPrefix(path, "~") {
		name = "a key"
	}
	switch {
	case strings.Contains(extra, "agent"):
		return name + " via agent"
	case strings.Contains(extra, "authenticator"):
		return name + " (YubiKey)"
	}
	return name
}

func shortErr(err error) string {
	s := err.Error()
	if len(s) > 100 {
		s = s[:99] + "…"
	}
	return s
}

// Cue is how the session screen keeps saying which kind of host this is,
// so a production shell never looks like a staging one. Inside slosh the
// pane's frame is tinted and its status carries the label; elsewhere the
// terminal background is tinted. All of it is undone when ssh exits.
type Cue struct {
	Label string // PROD, STAGING, ...
	Hex   string // frame tint
	Bg    string // terminal background tint, "" for none
}

// Proxy relays the terminal to the pty until ssh exits. It satisfies Bubble
// Tea's ExecCommand so the program can release the terminal around it.
// When inSlosh is set the pane's frame shows the host while connected.
func (s *Session) Proxy(inSlosh bool, cue *Cue) *Proxy {
	return &Proxy{s: s, inSlosh: inSlosh, cue: cue, in: os.Stdin, out: os.Stdout}
}

// Proxy is the handover phase of a Session.
type Proxy struct {
	s       *Session
	inSlosh bool
	cue     *Cue
	in      io.Reader
	out     io.Writer
}

// drainReply forwards what the user types while swallowing the one reply
// slosh sends back for an in-band shader request, which would otherwise be
// delivered to the remote shell as keystrokes.
func (p *Proxy) drainReply(cr io.Reader, deadline time.Duration) {
	type chunk struct {
		b   []byte
		err error
	}
	ch := make(chan chunk, 1)
	go func() {
		buf := make([]byte, 4096)
		n, err := cr.Read(buf)
		ch <- chunk{append([]byte(nil), buf[:n]...), err}
	}()
	var held []byte
	timer := time.NewTimer(deadline)
	defer timer.Stop()
	for {
		select {
		case c := <-ch:
			held = append(held, c.b...)
			if c.err != nil {
				p.s.pty.Write(stripReply(held))
				return
			}
			if start := bytes.Index(held, []byte("\x1b]5577;")); start >= 0 {
				if end := bytes.Index(held[start:], []byte("\x1b\\")); end >= 0 {
					p.s.pty.Write(stripReply(held))
					return
				}
			}
			go func() {
				buf := make([]byte, 4096)
				n, err := cr.Read(buf)
				ch <- chunk{append([]byte(nil), buf[:n]...), err}
			}()
		case <-timer.C:
			p.s.pty.Write(stripReply(held))
			return
		}
	}
}

// stripReply removes slosh OSC 5577 replies from typed input.
func stripReply(b []byte) []byte {
	for {
		start := bytes.Index(b, []byte("\x1b]5577;"))
		if start < 0 {
			return b
		}
		end := bytes.Index(b[start:], []byte("\x1b\\"))
		if end < 0 {
			return b[:start]
		}
		b = append(b[:start:start], b[start+end+2:]...)
	}
}

func (p *Proxy) SetStdin(r io.Reader)  { p.in = r }
func (p *Proxy) SetStdout(w io.Writer) { p.out = w }
func (p *Proxy) SetStderr(io.Writer)   {}

// Run blocks until ssh exits.
func (p *Proxy) Run() error {
	fd := os.Stdin.Fd()
	if f, ok := p.in.(*os.File); ok {
		fd = f.Fd()
	}
	state, err := term.MakeRaw(fd)
	if err == nil {
		defer term.Restore(fd, state)
	}
	resize := func() {
		if w, h, err := term.GetSize(fd); err == nil {
			pty.Setsize(p.s.pty, &pty.Winsize{Cols: uint16(w), Rows: uint16(h)})
		}
	}
	resize()
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)
	go func() {
		for range winch {
			resize()
		}
	}()

	status := "⇄ " + p.s.Alias
	if p.cue != nil {
		status = "⚠ " + p.cue.Label + " · " + p.s.Alias
	}
	if p.inSlosh {
		fmt.Fprintf(p.out, "\x1b]5577;1;status;%s\x1b\\", status)
		defer fmt.Fprint(p.out, "\x1b]5577;1;clear\x1b\\")
	} else if p.cue != nil && p.cue.Bg != "" {
		fmt.Fprintf(p.out, "\x1b]11;%s\x1b\\", p.cue.Bg)
		defer fmt.Fprint(p.out, "\x1b]111\x1b\\")
	}

	// Start the session on a blank screen: scroll whatever the pane showed
	// before (the berth prompt, earlier sessions) into scrollback rather
	// than erasing it, then put the cursor at the top.
	if _, rows, err := term.GetSize(fd); err == nil {
		fmt.Fprint(p.out, strings.Repeat("\r\n", rows)+"\x1b[H")
	}

	// Attach: everything that was not ssh's own chatter, then live.
	p.s.mu.Lock()
	replay := append(p.s.pending, []byte(p.s.partial)...)
	p.s.pending, p.s.partial = nil, ""
	if len(replay) > 0 {
		p.out.Write(replay)
	}
	p.s.out = p.out
	p.s.mu.Unlock()

	cr, err := cancelreader.NewReader(p.in)
	if err != nil {
		return err
	}
	tinted := false
	if p.inSlosh && p.cue != nil {
		// Needs in_band_shaders in the slosh config; refused otherwise, and
		// the reply (ok or error) is swallowed either way.
		// Only the foreground channel: the frame's lines and title take the
		// colour, the background stays the theme's, so it reads as a border
		// rather than a bar (what slosh's bell does too).
		fmt.Fprintf(p.out, "\x1b]5577;1;shader;chrome;tint color=\"%s\" amount=235 channel=\"fg\"\x1b\\", p.cue.Hex)
		p.drainReply(cr, 250*time.Millisecond)
		tinted = true
	}
	go io.Copy(p.s.pty, cr)
	<-p.s.done
	cr.Cancel()
	cr.Close()
	if tinted {
		fmt.Fprint(p.out, "\x1b]5577;1;shader;chrome;\x1b\\")
		cr2, err := cancelreader.NewReader(p.in)
		if err == nil {
			p.drainReply(cr2, 250*time.Millisecond)
			cr2.Cancel()
			cr2.Close()
		}
	}
	signal.Stop(winch)
	close(winch)
	return nil
}
