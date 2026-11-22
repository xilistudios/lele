package group

import "fmt"

type RoundRobinStrategy struct{}

func (s *RoundRobinStrategy) Name() string { return "round_robin" }

func (s *RoundRobinStrategy) Next(state *GroupState) ([]string, bool, error) {
	if stop, _ := StopReason(state); stop {
		return nil, true, nil
	}

	n := len(state.Participants)
	if n == 0 {
		return nil, false, fmt.Errorf("round_robin: no participants")
	}

	turns := len(state.Transcript)

	if state.Rounds > 0 && turns >= state.Rounds*n {
		return nil, true, nil
	}

	speaker := state.Participants[turns%n].AgentID
	return []string{speaker}, false, nil
}

func init() {
	RegisterStrategy("round_robin", func() Strategy { return &RoundRobinStrategy{} })
}
