package agent

import (
	"context"
	"testing"

	"github.com/xilistudios/lele/pkg/tools"
)

type interruptFixture struct {
	al         *AgentLoop
	sessionKey string
	subagents  *tools.SubagentManager
	bgm        *tools.BackgroundProcessManager
	taskID     string
	procID     string
	turnFired  chan struct{}
}

func newInterruptFixture(t *testing.T) *interruptFixture {
	t.Helper()

	sessionKey := "native:interrupt-fixture"
	sm := tools.NewSubagentManager(nil, "test-model", t.TempDir(), nil, 5)
	addRunningSubagent(t, sm, "subagent-interrupt", "", sessionKey)

	bgm := tools.NewBackgroundProcessManager()
	proc := startBackgroundProcForTest(t, bgm, "main", sessionKey)

	al := newCancelTestAgentLoop(t)
	al.toolCoordinator = newToolCoordinatorWithSubagents(al,
		map[string]*tools.SubagentManager{"main": sm},
		map[string]*tools.BackgroundProcessManager{"main": bgm},
		nil)
	al.providable = newAgentProvidable(al)

	turnFired := make(chan struct{}, 1)
	_, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	unregister := al.sessionManager.RegisterSessionCancel(sessionKey, func() {
		select {
		case turnFired <- struct{}{}:
		default:
		}
		cancel()
	})
	t.Cleanup(unregister)

	return &interruptFixture{
		al:         al,
		sessionKey: sessionKey,
		subagents:  sm,
		bgm:        bgm,
		taskID:     "subagent-interrupt",
		procID:     proc.ID,
		turnFired:  turnFired,
	}
}

func TestInterruptAgent_KeepsDetachedWorkRunning(t *testing.T) {
	f := newInterruptFixture(t)

	if response := f.al.providable.InterruptAgent(f.sessionKey); response == "" {
		t.Fatal("InterruptAgent returned an empty response")
	}

	select {
	case <-f.turnFired:
	default:
		t.Fatal("InterruptAgent did not cancel the running turn")
	}
	if s := runningTaskStatus(t, f.subagents, f.taskID); s != tools.SubagentStatusRunning {
		t.Errorf("subagent status = %q, want %q (detached work must survive a turn interrupt)", s, tools.SubagentStatusRunning)
	}
	if s := procStatus(t, f.bgm, f.procID); s != tools.BgExecStatusRunning {
		t.Errorf("background proc status = %q, want %q (detached work must survive a turn interrupt)", s, tools.BgExecStatusRunning)
	}
}

func TestStopAgent_StopsDetachedWork(t *testing.T) {
	f := newInterruptFixture(t)

	f.al.providable.StopAgent(f.sessionKey)

	if s := runningTaskStatus(t, f.subagents, f.taskID); s != tools.SubagentStatusCancelled {
		t.Errorf("subagent status = %q, want %q", s, tools.SubagentStatusCancelled)
	}
	if s := procStatus(t, f.bgm, f.procID); s != tools.BgExecStatusStopped {
		t.Errorf("background proc status = %q, want %q", s, tools.BgExecStatusStopped)
	}
}
