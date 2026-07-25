package group

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/xilistudios/lele/pkg/bus"
)

// runGroup is the main loop for a managed group. It runs synchronously;
// Start launches it in a goroutine.
func (gm *GroupManager) runGroup(ctx context.Context, mg *managedGroup) {
	state := mg.state

	// Persist the final state once this goroutine exits (LIFO — runs after the
	// publish/close defer below).  SaveGroup serialises mg.state which is only
	// mutated under gm.mu, but at this point the goroutine is done writing.
	defer gm.saveStateBestEffort(mg)

	strategy, err := NewStrategy(state.Strategy)
	if err != nil {
		gm.mu.Lock()
		state.Status = StatusError
		state.UpdatedAt = time.Now()
		gm.mu.Unlock()

		agentIDs := gm.agentIDsLocked(mg)
		gm.publishGroupStatus(mg, "error", agentIDs)
		mg.err = err
		close(mg.done)
		return
	}

	// Inject moderator decider if the strategy is moderator.
	if ms, ok := strategy.(*ModeratorStrategy); ok {
		gm.mu.Lock()
		d := gm.moderatorDecider
		gm.mu.Unlock()
		if d == nil {
			d = defaultModeratorDecider
		}
		ms.Decider = d
	}

	defer func() {
		// Publish group.complete if the group finished normally (not errored early or stopped).
		gm.mu.Lock()
		status := state.Status
		gm.mu.Unlock()

		if status == StatusDone || (status == StatusError && len(state.Transcript) > 0) {
			gm.publishGroupComplete(mg)
		}
		close(mg.done)
	}()

	const maxIterations = 1000
	for i := 0; i < maxIterations; i++ {
		// Check context cancellation.
		select {
		case <-ctx.Done():
			gm.mu.Lock()
			if state.Status == StatusRunning {
				state.Status = StatusStopped
				state.UpdatedAt = time.Now()
			}
			gm.mu.Unlock()
			return
		default:
		}

		// Hard-stop check.
		gm.mu.Lock()
		stop, reason := StopReason(state)
		gm.mu.Unlock()
		if stop {
			log.Printf("group %s: stopped (%s)", state.ID, reason)
			break
		}

		// Ask the strategy who speaks next.
		speakers, done, err := strategy.Next(state)
		if err != nil {
			gm.mu.Lock()
			state.Status = StatusError
			state.UpdatedAt = time.Now()
			mg.err = err
			gm.mu.Unlock()

			agentIDs := gm.agentIDsLocked(mg)
			gm.publishGroupStatus(mg, "error", agentIDs)
			return
		}
		if done {
			break
		}

		layer := 0
		if state.Strategy == "moa" {
			gm.mu.Lock()
			layer = MoACurrentLayer(state)
			gm.mu.Unlock()
		}

		// Execute speakers.
		if state.Parallel && len(speakers) > 1 {
			if err := gm.executeParallel(ctx, mg, speakers, layer); err != nil {
				if ctx.Err() != nil {
					// Context cancelled (Stop was called) — not a real error.
					gm.mu.Lock()
					if state.Status == StatusRunning {
						state.Status = StatusStopped
						state.UpdatedAt = time.Now()
					}
					gm.mu.Unlock()
					return
				}
				gm.mu.Lock()
				state.Status = StatusError
				state.UpdatedAt = time.Now()
				mg.err = err
				gm.mu.Unlock()

				agentIDs := gm.agentIDsLocked(mg)
				gm.publishGroupStatus(mg, "error", agentIDs)
				return
			}
		} else {
			if err := gm.executeSequential(ctx, mg, speakers, layer); err != nil {
				if ctx.Err() != nil {
					gm.mu.Lock()
					if state.Status == StatusRunning {
						state.Status = StatusStopped
						state.UpdatedAt = time.Now()
					}
					gm.mu.Unlock()
					return
				}
				gm.mu.Lock()
				state.Status = StatusError
				state.UpdatedAt = time.Now()
				mg.err = err
				gm.mu.Unlock()

				agentIDs := gm.agentIDsLocked(mg)
				gm.publishGroupStatus(mg, "error", agentIDs)
				return
			}
		}

		// Re-check context after batch.
		select {
		case <-ctx.Done():
			gm.mu.Lock()
			if state.Status == StatusRunning {
				state.Status = StatusStopped
				state.UpdatedAt = time.Now()
			}
			gm.mu.Unlock()
			return
		default:
		}
	}

	// If still running, mark done and compute synthesis.
	gm.mu.Lock()
	if state.Status == StatusRunning {
		state.Status = StatusDone
		state.UpdatedAt = time.Now()
	}
	synthesis := gm.synthesisLocked(mg)
	mg.result = synthesis
	gm.mu.Unlock()
}

// executeSequential runs speakers one by one in order.
func (gm *GroupManager) executeSequential(ctx context.Context, mg *managedGroup, speakers []string, layer int) error {
	for _, speaker := range speakers {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err := gm.executeSpeaker(ctx, mg, speaker, layer); err != nil {
			return err
		}
	}
	return nil
}

// executeParallel runs all speakers concurrently using errgroup.
func (gm *GroupManager) executeParallel(ctx context.Context, mg *managedGroup, speakers []string, layer int) error {
	g, gctx := errgroup.WithContext(ctx)

	for _, speaker := range speakers {
		g.Go(func() error {
			return gm.executeSpeaker(gctx, mg, speaker, layer)
		})
	}

	return g.Wait()
}

// executeSpeaker resolves an agent, builds the turn request, executes it,
// records the turn, and publishes a group.turn event.
func (gm *GroupManager) executeSpeaker(ctx context.Context, mg *managedGroup, speaker string, layer int) error {
	agCtx, ok := gm.resolve(speaker)
	if !ok {
		log.Printf("group %s: speaker %q not resolved, skipping", mg.state.ID, speaker)
		return nil // skip unknown speakers rather than error
	}

	gm.mu.Lock()
	state := mg.state
	p, _ := state.ParticipantByAgent(speaker)
	self := Participant{
		AgentID: speaker,
		Role:    p.Role,
		Label:   agCtx.Name,
	}
	if self.Label == "" {
		self.Label = speaker
	}

	sysPrompt := BuildTurnSystemPrompt(agCtx.SystemPrompt, self, state.Participants, state.Task)
	transcript := RenderTranscript(state.Transcript)
	instruction := instructionFor(state, self, layer)
	gm.mu.Unlock()

	req := TurnRequest{
		GroupID:       state.ID,
		Speaker:       speaker,
		SystemPrompt:  sysPrompt,
		Transcript:    transcript,
		Instruction:   instruction,
		MaxTokens:     state.MaxTokensPerTurn,
		EnableTools:   true,
		OriginChannel: mg.originCh,
		OriginChatID:  mg.originChat,
	}

	content, tokens, err := gm.executor(ctx, req)
	if err != nil {
		return fmt.Errorf("turn %s: %w", speaker, err)
	}

	gm.mu.Lock()
	turn := Turn{
		Index:     len(state.Transcript),
		Layer:     layer,
		Speaker:   speaker,
		Label:     self.Label,
		Content:   content,
		CreatedAt: time.Now(),
		Tokens:    tokens,
	}
	state.AddTurn(turn)
	role := self.Role
	gm.mu.Unlock()

	// Publish group.turn event.
	gm.publishTurn(mg, turn, role)

	return nil
}

// synthesisLocked computes the final synthesis text. Caller must hold gm.mu.
func (gm *GroupManager) synthesisLocked(mg *managedGroup) string {
	state := mg.state
	if len(state.Transcript) == 0 {
		return ""
	}

	if state.Strategy == "moa" {
		agg := MoAAggregator(state)
		// Walk backwards to find the aggregator's last turn.
		for i := len(state.Transcript) - 1; i >= 0; i-- {
			if state.Transcript[i].Speaker == agg {
				return state.Transcript[i].Content
			}
		}
	}

	return state.Transcript[len(state.Transcript)-1].Content
}

// publishTurn publishes a group.turn event.
func (gm *GroupManager) publishTurn(mg *managedGroup, turn Turn, role string) {
	gm.publish(bus.OutboundMessage{
		Channel:        mg.originCh,
		ChatID:         mg.originChat,
		Event:          "group.turn",
		Content:        turn.Content,
		IsIntermediate: true,
		Metadata: map[string]string{
			"group_id":   mg.state.ID,
			"speaker":    turn.Speaker,
			"label":      turn.Label,
			"role":       role,
			"layer":      strconv.Itoa(turn.Layer),
			"turn_index": strconv.Itoa(turn.Index),
		},
	})
}

// publishGroupComplete publishes the final group.complete event.
func (gm *GroupManager) publishGroupComplete(mg *managedGroup) {
	gm.mu.Lock()
	state := mg.state
	synthesis := mg.result

	// Calculate max layer + 1 (or 1 if no turns).
	layers := 1
	if len(state.Transcript) > 0 {
		maxLayer := 0
		for _, t := range state.Transcript {
			if t.Layer > maxLayer {
				maxLayer = t.Layer
			}
		}
		layers = maxLayer + 1
	}
	totalTokens := state.TotalTokens
	strategy := state.Strategy
	groupID := state.ID
	gm.mu.Unlock()

	gm.publish(bus.OutboundMessage{
		Channel:        mg.originCh,
		ChatID:         mg.originChat,
		Event:          "group.complete",
		Content:        synthesis,
		IsIntermediate: false,
		Metadata: map[string]string{
			"group_id":     groupID,
			"strategy":     strategy,
			"layers":       strconv.Itoa(layers),
			"total_tokens": strconv.Itoa(totalTokens),
		},
	})
}

// instructionFor returns the turn instruction based on the speaker's role
// and the group strategy.
func instructionFor(state *GroupState, self Participant, layer int) string {
	switch {
	case self.Role == RoleAggregator || (state.Strategy == "moa" && self.AgentID == MoAAggregator(state)):
		return "Synthesize the proposals above into a single best response to the objective."
	case self.Role == RoleProposer || (state.Strategy == "moa" && self.Role != RoleAggregator):
		return fmt.Sprintf("Propose a solution to the objective, building on the panel's prior contributions. Offer your perspective as %s.", self.Role)
	case self.Role == RoleModerator:
		return "Synthesize the proposals above into a single best response to the objective."
	default:
		return "Contribute your perspective to the panel's objective, building on what others have said."
	}
}

// defaultModeratorDecider is a simple round-robin moderator that cycles through
// participants and respects MaxTurns as a hard stop.
func defaultModeratorDecider(state *GroupState) (string, bool, error) {
	turns := len(state.Transcript)
	n := len(state.Participants)
	if n == 0 {
		return "", true, nil
	}
	if state.MaxTurns > 0 && turns >= state.MaxTurns {
		return "", true, nil
	}
	return state.Participants[turns%n].AgentID, false, nil
}
