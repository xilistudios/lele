package group

import (
	"testing"
)

// testStrategy is a minimal Strategy implementation used only in tests.
type testStrategy struct {
	name string
}

func (s *testStrategy) Name() string { return s.name }

func (s *testStrategy) Next(state *GroupState) ([]string, bool, error) {
	if len(state.Transcript) >= 3 {
		return nil, true, nil
	}
	return []string{"test-agent"}, false, nil
}

func TestRegisterAndNewStrategy(t *testing.T) {
	// Register a test strategy. Since the registry is global and tests may
	// run in the same process, use a unique name to avoid collisions with
	// other tests or future parallel tests.
	const name = "test_register_new_unique"
	RegisterStrategy(name, func() Strategy {
		return &testStrategy{name: name}
	})

	s, err := NewStrategy(name)
	if err != nil {
		t.Fatalf("NewStrategy(%q) error: %v", name, err)
	}
	if s.Name() != name {
		t.Errorf("Name() = %q, want %q", s.Name(), name)
	}

	// Verify Next works on a fresh state
	state := &GroupState{ID: "g1", Status: StatusRunning}
	speakers, done, err := s.Next(state)
	if err != nil {
		t.Fatalf("Next() error: %v", err)
	}
	if done {
		t.Error("Next() on empty state returned done=true, want false")
	}
	if len(speakers) != 1 || speakers[0] != "test-agent" {
		t.Errorf("Next() speakers = %v, want [test-agent]", speakers)
	}
}

func TestNewStrategy_NotFound(t *testing.T) {
	_, err := NewStrategy("definitely_does_not_exist_strategy_xyz")
	if err == nil {
		t.Fatal("NewStrategy for non-existent strategy returned nil error, want error")
	}
}

func TestRegisterStrategy_PanicsOnDuplicate(t *testing.T) {
	const name = "test_duplicate_panic_unique"
	RegisterStrategy(name, func() Strategy {
		return &testStrategy{name: name}
	})

	defer func() {
		r := recover()
		if r == nil {
			t.Error("RegisterStrategy with duplicate name did not panic, want panic")
		}
	}()

	// Second registration with the same name should panic
	RegisterStrategy(name, func() Strategy {
		return &testStrategy{name: name}
	})
}

func TestStrategyInterfaceCompliance(t *testing.T) {
	// Compile-time check that testStrategy implements Strategy
	var _ Strategy = (*testStrategy)(nil)
}
