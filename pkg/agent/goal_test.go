package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestGoalManager_SetAndGet(t *testing.T) {
	dir := t.TempDir()
	gm := NewGoalManager(dir)

	goal := gm.Set("session1", "Fix all lint errors", 10)
	if goal == nil {
		t.Fatal("Set returned nil")
	}
	if goal.Text != "Fix all lint errors" {
		t.Errorf("Text = %q, want %q", goal.Text, "Fix all lint errors")
	}
	if goal.MaxTurns != 10 {
		t.Errorf("MaxTurns = %d, want 10", goal.MaxTurns)
	}
	if goal.Status != GoalActive {
		t.Errorf("Status = %q, want %q", goal.Status, GoalActive)
	}

	got := gm.Get("session1")
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.Text != "Fix all lint errors" {
		t.Errorf("Get Text = %q, want %q", got.Text, "Fix all lint errors")
	}
}

func TestGoalManager_DefaultMaxTurns(t *testing.T) {
	gm := NewGoalManager("")
	goal := gm.Set("s1", "test", 0)
	if goal.MaxTurns != DefaultGoalMaxTurns {
		t.Errorf("MaxTurns = %d, want default %d", goal.MaxTurns, DefaultGoalMaxTurns)
	}
}

func TestGoalManager_PauseResume(t *testing.T) {
	gm := NewGoalManager("")
	gm.Set("s1", "test goal", 5)

	if !gm.IsActive("s1") {
		t.Fatal("expected active after Set")
	}

	if !gm.Pause("s1") {
		t.Fatal("Pause returned false")
	}
	if gm.IsActive("s1") {
		t.Fatal("expected not active after Pause")
	}

	if !gm.Resume("s1") {
		t.Fatal("Resume returned false")
	}
	if !gm.IsActive("s1") {
		t.Fatal("expected active after Resume")
	}
}

func TestGoalManager_Clear(t *testing.T) {
	gm := NewGoalManager("")
	gm.Set("s1", "test goal", 5)

	if !gm.Clear("s1") {
		t.Fatal("Clear returned false")
	}
	if gm.Get("s1") != nil {
		t.Fatal("expected nil after Clear")
	}
	if gm.Clear("s1") {
		t.Fatal("second Clear should return false")
	}
}

func TestGoalManager_IncrementTurn_BudgetExhaustion(t *testing.T) {
	gm := NewGoalManager("")
	gm.Set("s1", "test", 3)

	if exhausted := gm.IncrementTurn("s1"); exhausted {
		t.Fatal("turn 1 should not exhaust budget of 3")
	}
	if exhausted := gm.IncrementTurn("s1"); exhausted {
		t.Fatal("turn 2 should not exhaust budget of 3")
	}
	if exhausted := gm.IncrementTurn("s1"); !exhausted {
		t.Fatal("turn 3 should exhaust budget of 3")
	}

	goal := gm.Get("s1")
	if goal.Status != GoalBlocked {
		t.Errorf("Status = %q, want %q", goal.Status, GoalBlocked)
	}
}

func TestGoalManager_MarkDone(t *testing.T) {
	gm := NewGoalManager("")
	gm.Set("s1", "test", 10)
	gm.IncrementTurn("s1")
	gm.MarkDone("s1")

	goal := gm.Get("s1")
	if goal.Status != GoalDone {
		t.Errorf("Status = %q, want %q", goal.Status, GoalDone)
	}
	if gm.IsActive("s1") {
		t.Fatal("done goal should not be active")
	}
}

func TestGoalManager_Persistence(t *testing.T) {
	dir := t.TempDir()

	// Create and persist a goal
	gm1 := NewGoalManager(dir)
	gm1.Set("telegram:12345", "Build the feature", 15)

	// Load from disk in a new manager
	gm2 := NewGoalManager(dir)
	goal := gm2.Get("telegram:12345")
	if goal == nil {
		t.Fatal("goal not loaded from disk")
	}
	if goal.Text != "Build the feature" {
		t.Errorf("Text = %q, want %q", goal.Text, "Build the feature")
	}
	if goal.MaxTurns != 15 {
		t.Errorf("MaxTurns = %d, want 15", goal.MaxTurns)
	}
}

func TestGoalManager_PersistenceClearRemovesFile(t *testing.T) {
	dir := t.TempDir()
	gm := NewGoalManager(dir)
	gm.Set("sess1", "goal", 5)

	// Verify file exists
	files, _ := os.ReadDir(dir)
	if len(files) == 0 {
		t.Fatal("expected persisted file")
	}

	gm.Clear("sess1")

	// Verify file removed
	files, _ = os.ReadDir(dir)
	if len(files) != 0 {
		t.Fatalf("expected no files after clear, got %d", len(files))
	}
}

func TestGoalManager_DoneGoalsNotRestored(t *testing.T) {
	dir := t.TempDir()
	gm1 := NewGoalManager(dir)
	gm1.Set("s1", "done goal", 5)
	gm1.MarkDone("s1")

	// Load from disk - done goals should not be restored
	gm2 := NewGoalManager(dir)
	if gm2.Get("s1") != nil {
		t.Fatal("done goal should not be restored from disk")
	}
}

func TestGoalManager_FormatStatus(t *testing.T) {
	gm := NewGoalManager("")
	goal := gm.Set("s1", "Fix tests", 20)
	status := goal.FormatStatus()
	if status == "" {
		t.Fatal("FormatStatus returned empty string")
	}
	if !goalContains(status, "Fix tests") {
		t.Errorf("status should contain goal text, got: %s", status)
	}
	if !goalContains(status, "active") {
		t.Errorf("status should contain 'active', got: %s", status)
	}
}

func TestContainsDone(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"DONE", true},
		{"done", true},
		{"DONE.", true},
		{"CONTINUE", false},
		{"NOT DONE", false},
		{"I think it's done", true},
		{"", false},
	}
	for _, tt := range tests {
		if got := containsDone(tt.input); got != tt.want {
			t.Errorf("containsDone(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestSanitizeFileName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"telegram:12345", "telegram_12345"},
		{"native:abc-def", "native_abc-def"},
		{"simple", "simple"},
		{"a/b\\c:d", "a_b_c_d"},
	}
	for _, tt := range tests {
		if got := sanitizeFileName(tt.input); got != tt.want {
			t.Errorf("sanitizeFileName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// mockGoalJudge is a test judge that always returns a fixed result.
type mockGoalJudge struct {
	done bool
}

func (m *mockGoalJudge) JudgeGoal(_ context.Context, _, _, _ string) (bool, string, error) {
	if m.done {
		return true, "DONE", nil
	}
	return false, "CONTINUE", nil
}

func TestGoalManager_SetJudge(t *testing.T) {
	gm := NewGoalManager("")
	judge := &mockGoalJudge{done: true}
	gm.SetJudge(judge)

	if gm.judge == nil {
		t.Fatal("judge not set")
	}

	isDone, answer, err := gm.judge.JudgeGoal(context.Background(), "key", "test", "response")
	if err != nil {
		t.Fatalf("JudgeGoal error: %v", err)
	}
	if !isDone {
		t.Error("expected isDone=true")
	}
	if answer != "DONE" {
		t.Errorf("answer = %q, want DONE", answer)
	}
}

func TestGoalManager_MultipleSessions(t *testing.T) {
	gm := NewGoalManager("")
	gm.Set("s1", "goal one", 10)
	gm.Set("s2", "goal two", 20)

	g1 := gm.Get("s1")
	g2 := gm.Get("s2")
	if g1.Text != "goal one" || g2.Text != "goal two" {
		t.Error("sessions should have independent goals")
	}

	gm.Clear("s1")
	if gm.Get("s1") != nil {
		t.Error("s1 should be cleared")
	}
	if gm.Get("s2") == nil {
		t.Error("s2 should still exist")
	}
}

func TestGoalManager_PersistDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "goals")
	gm := NewGoalManager(dir)
	gm.Set("s1", "nested goal", 5)

	// Verify nested dir was created
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Fatal("nested goals dir was not created")
	}
}

func goalContains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && goalContainsStr(s, substr))
}

func goalContainsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
