package group

import (
	"strings"
	"testing"
)

func TestRenderTranscript_Empty(t *testing.T) {
	if got := RenderTranscript(nil); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
	if got := RenderTranscript([]Turn{}); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestRenderTranscript_SingleTurn(t *testing.T) {
	turns := []Turn{
		{Index: 0, Layer: 0, Speaker: "alice", Label: "Alice", Content: "Hello world"},
	}
	got := RenderTranscript(turns)
	want := "[Alice]: Hello world"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRenderTranscript_MultipleTurnsSameLayer(t *testing.T) {
	turns := []Turn{
		{Index: 0, Layer: 0, Speaker: "alice", Label: "Alice", Content: "First"},
		{Index: 1, Layer: 0, Speaker: "bob", Label: "Bob", Content: "Second"},
		{Index: 2, Layer: 0, Speaker: "alice", Label: "Alice", Content: "Third"},
	}
	got := RenderTranscript(turns)
	want := "[Alice]: First\n[Bob]: Second\n[Alice]: Third"
	if got != want {
		t.Errorf("got:\n%s\n\nwant:\n%s", got, want)
	}
	// No layer separator should appear.
	if strings.Contains(got, "--- layer") {
		t.Errorf("unexpected layer separator in single-layer transcript:\n%s", got)
	}
}

func TestRenderTranscript_TwoLayers(t *testing.T) {
	turns := []Turn{
		{Index: 0, Layer: 0, Speaker: "alice", Label: "Alice", Content: "L0-A"},
		{Index: 1, Layer: 0, Speaker: "bob", Label: "Bob", Content: "L0-B"},
		{Index: 2, Layer: 1, Speaker: "carol", Label: "Carol", Content: "L1-C"},
		{Index: 3, Layer: 1, Speaker: "dave", Label: "Dave", Content: "L1-D"},
	}
	got := RenderTranscript(turns)

	// Layer separator must appear once for layer 1.
	if !strings.Contains(got, "--- layer 0 ---") {
		t.Errorf("expected layer 0 separator:\n%s", got)
	}
	if !strings.Contains(got, "--- layer 1 ---") {
		t.Errorf("expected layer 1 separator:\n%s", got)
	}
	// Check order.
	idx0 := strings.Index(got, "--- layer 0 ---")
	idx1 := strings.Index(got, "--- layer 1 ---")
	if idx0 >= idx1 {
		t.Errorf("layer 0 separator should come before layer 1:\n%s", got)
	}
	if !strings.Contains(got, "[Alice]: L0-A") {
		t.Errorf("missing Alice turn:\n%s", got)
	}
	if !strings.Contains(got, "[Dave]: L1-D") {
		t.Errorf("missing Dave turn:\n%s", got)
	}
}

func TestGroupRoleAnnex_Basic(t *testing.T) {
	self := Participant{AgentID: "alice", Role: RoleProposer, Label: "Alice"}
	participants := []Participant{
		self,
		{AgentID: "bob", Role: RoleAggregator, Label: "Bob"},
		{AgentID: "carol", Role: RoleCritic, Label: "Carol"},
	}
	task := "Design a microservices architecture"

	annex := GroupRoleAnnex(self, participants, task)

	// Must contain self role.
	if !strings.Contains(annex, "proposer") {
		t.Errorf("annex missing self role:\n%s", annex)
	}
	if !strings.Contains(annex, "(you)") {
		t.Errorf("annex missing (you) marker:\n%s", annex)
	}
	// Must list all participants.
	if !strings.Contains(annex, "Alice") || !strings.Contains(annex, "Bob") || !strings.Contains(annex, "Carol") {
		t.Errorf("annex missing participant names:\n%s", annex)
	}
	// Must include roles.
	if !strings.Contains(annex, "aggregator") {
		t.Errorf("annex missing aggregator role:\n%s", annex)
	}
	if !strings.Contains(annex, "critic") {
		t.Errorf("annex missing critic role:\n%s", annex)
	}
	// Must include task.
	if !strings.Contains(annex, task) {
		t.Errorf("annex missing task:\n%s", annex)
	}
}

func TestGroupRoleAnnex_ParticipantWithEmptyRole(t *testing.T) {
	self := Participant{AgentID: "x", Role: "", Label: "X"}
	participants := []Participant{self}
	task := "do something"

	annex := GroupRoleAnnex(self, participants, task)
	if !strings.Contains(annex, "X") {
		t.Errorf("annex should list participant even with empty role:\n%s", annex)
	}
}

func TestBuildTurnSystemPrompt_WithPersona(t *testing.T) {
	persona := "You are a helpful assistant."
	self := Participant{AgentID: "a", Role: RoleProposer, Label: "Agent A"}
	participants := []Participant{self}
	task := "write tests"

	got := BuildTurnSystemPrompt(persona, self, participants, task)

	if !strings.HasPrefix(got, persona) {
		t.Errorf("expected persona prefix, got:\n%s", got)
	}
	if !strings.Contains(got, "---") {
		t.Errorf("expected separator between persona and annex:\n%s", got)
	}
	if !strings.Contains(got, task) {
		t.Errorf("expected task in annex:\n%s", got)
	}
}

func TestBuildTurnSystemPrompt_EmptyPersona(t *testing.T) {
	self := Participant{AgentID: "a", Role: RoleProposer, Label: "Agent A"}
	participants := []Participant{self}
	task := "do stuff"

	got := BuildTurnSystemPrompt("", self, participants, task)

	// Should not start with a separator.
	if strings.HasPrefix(got, "---") {
		t.Errorf("unexpected leading separator when persona is empty:\n%s", got)
	}
	if !strings.Contains(got, task) {
		t.Errorf("expected task in annex:\n%s", got)
	}
	if !strings.Contains(got, "Agent A") {
		t.Errorf("expected agent name:\n%s", got)
	}
}
