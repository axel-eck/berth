// Package probe asks ssh and the network about a host without connecting.
package probe

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"
)

// SSH is the ssh binary to ask; see session.SSH.
var SSH = "ssh"

// ConfigFile, when not empty, is passed as -F so a `-config` file is what
// ssh resolves and connects with, not just what is listed. Empty means
// ssh's own default, which also keeps /etc/ssh/ssh_config in play.
var ConfigFile string

// Args prefixes ssh arguments with the config file when one was chosen.
func Args(args ...string) []string {
	if ConfigFile != "" {
		return append([]string{"-F", ConfigFile}, args...)
	}
	return args
}

// CheckConfig parses a config file the way ssh would and returns what it
// complains about, so a bad edit is caught before it replaces the real one.
func CheckConfig(path string) error {
	out, err := exec.Command(SSH, "-G", "-F", path, "--", "berth.invalid").CombinedOutput()
	if err == nil {
		return nil
	}
	msg := strings.TrimSpace(string(out))
	if i := strings.LastIndex(msg, "\n"); i >= 0 {
		msg = msg[i+1:]
	}
	return fmt.Errorf("ssh refuses the file: %s", msg)
}

// Effective is what `ssh -G alias` resolves: every option after config,
// Match blocks, includes and defaults are applied. Keys are lowercase;
// cumulative options (identityfile, setenv, localforward) keep every value.
type Effective map[string][]string

// Get is the first value, which is the one ssh uses.
func (e Effective) Get(key string) string {
	if v := e[key]; len(v) > 0 {
		return v[0]
	}
	return ""
}

// Resolve runs ssh -G. It never touches the network.
func Resolve(alias string) (Effective, error) {
	out, err := exec.Command(SSH, Args("-G", "--", alias)...).Output()
	if err != nil {
		return nil, err
	}
	eff := Effective{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), " ")
		if !ok {
			k, v = sc.Text(), ""
		}
		eff[k] = append(eff[k], v)
	}
	return eff, nil
}

// Reach opens and closes a TCP connection to host:port and reports how
// long it took: a cheap "is it up" that needs no key and no touch.
func Reach(host, port string, timeout time.Duration) (time.Duration, error) {
	start := time.Now()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), timeout)
	if err != nil {
		return 0, err
	}
	conn.Close()
	return time.Since(start), nil
}

// MasterArgs are the ssh arguments for a control master on socket path:
// authenticate, fork to the background, no command.
func MasterArgs(path string) []string { return []string{"-M", "-S", path, "-f", "-N"} }

// SocketUp asks the master behind path whether it is alive. No network.
func SocketUp(alias, path string) bool {
	if _, err := os.Stat(path); err != nil {
		return false
	}
	return exec.Command(SSH, Args("-S", path, "-O", "check", "--", alias)...).Run() == nil
}

// SocketClose asks the master behind path to exit.
func SocketClose(alias, path string) error {
	out, err := exec.Command(SSH, Args("-S", path, "-O", "exit", "--", alias)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	return nil
}

// DefaultSocket is where berth puts a host's control socket unless told
// otherwise: the spelling tools commonly ask for.
func DefaultSocket(alias string) string { return "/tmp/ssh-" + alias }
