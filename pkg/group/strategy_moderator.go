package group

import "fmt"

// ModeratorDecider is a function that the moderator strategy delegates to
// for deciding who speaks next and when to stop.
type ModeratorDecider func(state *GroupState) (speaker string, done bool, err error)

// ModeratorStrategy delegates speaker selection to a ModeratorDecider function.
// The decider is typically backed by an LLM that evaluates the conversation
// and decides who should speak next and whether convergence has been reached.
type ModeratorStrategy struct {
	Decider ModeratorDecider
}

func (s *ModeratorStrategy) Name() string { return "moderator" }

func (s *ModeratorStrategy) Next(state *GroupState) ([]string, bool, error) {
	// Hard-stop limits always apply, regardless of what the decider wants.
	if stop, _ := StopReason(state); stop {
		return nil, true, nil
	}

	if s.Decider == nil {
		return nil, false, fmt.Errorf("moderator strategy requires a Decider")
	}

	speaker, done, err := s.Decider(state)
	if err != nil {
		return nil, false, fmt.Errorf("moderator decider error: %w", err)
	}
	if done {
		return nil, true, nil
	}
	return []string{speaker}, false, nil
}

func init() {
	RegisterStrategy("moderator", func() Strategy { return &ModeratorStrategy{} })
}
