package group

// MoAStrategy implements the "Mixture of Agents" pattern:
// N proposers speak (in parallel or sequence) per layer, then the
// aggregator/moderator synthesizes. This repeats for Rounds layers.
type MoAStrategy struct{}

func (s *MoAStrategy) Name() string { return "moa" }

// MoAProposers returns the participants that should propose in each layer.
// If any participant has Role==RoleProposer, only those are returned.
// Otherwise, all participants EXCEPT the moderator/aggregator are proposers.
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

	// Fallback: all except the moderator/aggregator.
	agg := MoAAggregator(state)
	var result []Participant
	for _, p := range state.Participants {
		if p.AgentID != agg {
			result = append(result, p)
		}
	}
	return result
}

// MoAAggregator returns the AgentID of the aggregator/moderator.
// Priority: state.Moderator field > participant with RoleAggregator > first participant.
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

// MoACurrentLayer returns the number of fully completed layers.
// A layer L is complete when the aggregator has at least one turn with Layer==L.
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

	// Rounds cap: if we've completed enough layers, done.
	if state.Rounds > 0 && layer >= state.Rounds {
		return nil, true, nil
	}

	proposers := MoAProposers(state)
	aggregator := MoAAggregator(state)

	// Build set of proposers who have already spoken in this layer.
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

	// Find remaining proposers for this layer.
	var remaining []string
	for _, p := range proposers {
		if !spoken[p.AgentID] {
			remaining = append(remaining, p.AgentID)
		}
	}

	// If there are proposers left, return them as a batch.
	if len(remaining) > 0 {
		return remaining, false, nil
	}

	// All proposers have spoken; check if aggregator has spoken in this layer.
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

	// Should not reach here: if aggregator has spoken, MoACurrentLayer should
	// have advanced. Return done as a safety fallback.
	return nil, true, nil
}

func init() {
	RegisterStrategy("moa", func() Strategy { return &MoAStrategy{} })
}
