package group

import "fmt"

type ModeratorDecider func(state *GroupState) (speaker string, done bool, err error)

type ModeratorStrategy struct {
	Decider ModeratorDecider
}

func (s *ModeratorStrategy) Name() string { return "moderator" }

func (s *ModeratorStrategy) Next(state *GroupState) ([]string, bool, error) {
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
