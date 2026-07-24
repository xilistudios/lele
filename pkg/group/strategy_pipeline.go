package group

// PipelineStrategy runs each participant in sequence exactly once.
// Speaker order matches the Participants slice order (A→B→C).
// It stops after the last participant has spoken.
type PipelineStrategy struct{}

func (s *PipelineStrategy) Name() string { return "pipeline" }

func (s *PipelineStrategy) Next(state *GroupState) ([]string, bool, error) {
	if stop, _ := StopReason(state); stop {
		return nil, true, nil
	}

	n := len(state.Participants)
	turns := len(state.Transcript)

	// Every participant has spoken once → done.
	if turns >= n {
		return nil, true, nil
	}

	speaker := state.Participants[turns].AgentID
	return []string{speaker}, false, nil
}

func init() {
	RegisterStrategy("pipeline", func() Strategy { return &PipelineStrategy{} })
}
