package group

type MoAStrategy struct{}

func (s *MoAStrategy) Name() string { return "moa" }

func MoAProposers(state *GroupState) []Participant {
	var proposers []Participant
	for _, p := range state.Participants {
		if p.Role == RoleProposer {
			proposers = append(proposers, p)
		}
	}
	if len(proposers) > 0 {
		return proposers
	}

	agg := MoAAggregator(state)
	var result []Participant
	for _, p := range state.Participants {
		if p.AgentID != agg {
			result = append(result, p)
		}
	}
	return result
}

func MoAAggregator(state *GroupState) string {
	if state.Moderator != "" {
		return state.Moderator
	}
	for _, p := range state.Participants {
		if p.Role == RoleAggregator {
			return p.AgentID
		}
	}
	if len(state.Participants) > 0 {
		return state.Participants[0].AgentID
	}
	return ""
}

func MoACurrentLayer(state *GroupState) int {
	layer := 0
	agg := MoAAggregator(state)
	for {
		found := false
		for _, t := range state.Transcript {
			if t.Speaker == agg && t.Layer == layer {
				found = true
				break
			}
		}
		if !found {
			break
		}
		layer++
	}
	return layer
}

func (s *MoAStrategy) Next(state *GroupState) ([]string, bool, error) {
	if stop, _ := StopReason(state); stop {
		return nil, true, nil
	}

	layer := MoACurrentLayer(state)

	if state.Rounds > 0 && layer >= state.Rounds {
		return nil, true, nil
	}

	proposers := MoAProposers(state)
	aggregator := MoAAggregator(state)

	spoken := make(map[string]bool)
	for _, t := range state.Transcript {
		if t.Layer == layer {
			for _, p := range proposers {
				if t.Speaker == p.AgentID {
					spoken[p.AgentID] = true
				}
			}
		}
	}

	var remaining []string
	for _, p := range proposers {
		if !spoken[p.AgentID] {
			remaining = append(remaining, p.AgentID)
		}
	}

	if len(remaining) > 0 {
		return remaining, false, nil
	}

	aggSpoken := false
	for _, t := range state.Transcript {
		if t.Layer == layer && t.Speaker == aggregator {
			aggSpoken = true
			break
		}
	}

	if !aggSpoken {
		return []string{aggregator}, false, nil
	}

	return nil, true, nil
}

func init() {
	RegisterStrategy("moa", func() Strategy { return &MoAStrategy{} })
}
