package probe

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// A FIDO key (YubiKey) that is not plugged in makes ssh fail late and
// loudly: "signing failed", then Permission denied. Asking the system
// first is cheap, so the card can ask for the key before ssh runs.
//
// fido2-token comes with libfido2, which the FIDO-capable OpenSSH builds
// depend on, and lists exactly what ssh will be able to use. ykman is the
// fallback for people who installed YubiKey Manager and nothing else.
var tokenListers = [][]string{{"fido2-token", "-L"}, {"ykman", "list"}}

var (
	tokenOnce   sync.Once
	tokenLister []string
)

func findTokenLister() []string {
	tokenOnce.Do(func() {
		for _, cmd := range tokenListers {
			if path, err := exec.LookPath(cmd[0]); err == nil {
				tokenLister = append([]string{path}, cmd[1:]...)
				return
			}
		}
	})
	return tokenLister
}

// TokenCheckable says whether a security key can be looked for at all.
// Without a lister on PATH the check is skipped, never guessed.
func TokenCheckable() bool { return findTokenLister() != nil }

// TokenPresent reports whether a security key is plugged in. Both listers
// print one line per device and nothing when there is none; a lister that
// hangs or fails is read as "present" so a glitch never blocks a connection.
func TokenPresent() bool {
	lister := findTokenLister()
	if lister == nil {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, lister[0], lister[1:]...).Output()
	if err != nil {
		return true
	}
	return strings.TrimSpace(string(out)) != ""
}
