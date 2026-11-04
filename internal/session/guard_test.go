package session

import (
	"os/exec"
	"testing"
	"time"
)

func TestGuardKillsWhenTheKeyGoes(t *testing.T) {
	readings := make(chan bool, 3)
	readings <- true
	readings <- true
	readings <- false
	s := &Session{cmd: &exec.Cmd{}, done: make(chan struct{})}
	s.guard = func() bool { return <-readings }
	finished := make(chan struct{})
	go func() { s.guardKey(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("the guard did not act on the key going away")
	}
	if s.EndedBy() != KeyRemoved {
		t.Errorf("EndedBy = %q, want %q", s.EndedBy(), KeyRemoved)
	}
}

func TestGuardIsNotArmedWithoutTheKey(t *testing.T) {
	s := &Session{cmd: &exec.Cmd{}, done: make(chan struct{})}
	calls := 0
	s.guard = func() bool { calls++; return false }
	s.guardKey()
	if calls != 1 || s.EndedBy() != "" {
		t.Errorf("a key absent at handover should not be guarded: calls=%d endedBy=%q", calls, s.EndedBy())
	}
}
