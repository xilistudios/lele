package agent
import "testing"
func TestDbg(t *testing.T){
	al := newCovTestLoop(t)
	subKey := "subagent:scan-task"
	coder, _ := al.registry.GetAgent("coder")
	main, _ := al.registry.GetAgent("main")
	coder.Sessions.AddMessage(subKey, "user", "sub msg")
	t.Logf("coder==main Sessions? %v", coder.Sessions == main.Sessions)
	h := main.Sessions.GetHistoryView(subKey)
	t.Logf("main hist len: %d", len(h))
}
