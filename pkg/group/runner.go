package group

import (
	"context"
	"fmt"
	"log"
	"runtime/debug"
	"strconv"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/xilistudios/lele/pkg/bus"
)

// maxGroupIterations is the safety valve on the run loop. Reaching it without
// the strategy or a convergence rule having stopped the group is treated as an
// error, never as a silent success.
const maxGroupIterations = 1000

// runGroup is the main loop for a managed group. It runs synchronously;
// Start launches it in a goroutine.
//
// Every exit path — natural completion, Stop(), parent-context cancellation,
// strategy error, hard-stop, iteration exhaustion and panic — funnels through
// gm.finalize exactly once, so clients always receive precisely one terminal
// signal pair (a terminal group.status plus one group.complete) and mg.done is
// always closed.
func (gm *GroupManager) runGroup(ctx context.Context, mg *managedGroup) {
	state := mg.state

	// Persist the final state once this goroutine exits.  Registered FIRST so
	// that LIFO defer ordering runs it LAST — after the panic-recovery defer
	// below has already called finalize and published the terminal pair.
	// SaveGroup serialises mg.state which is only mutated under gm.mu, but at
	// this point the goroutine is done writing.
	defer gm.saveStateBestEffort(mg)

	// Contain a panic in the loop: turn it into the single terminal signal
	// pair instead of tearing down the process. Registered SECOND so it runs
	// BEFORE the state save.
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		stack := debug.Stack()
		log.Printf("group %s: panic in run loop: %v\n%s", state.ID, r, stack)
		gm.finalize(mg, StatusError, fmt.Errorf("group panic: %v\n%s", r, stack))
	}()

	strategy, err := NewStrategy(state.Strategy)
	if err != nil {
		gm.finalize(mg, StatusError, err)
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

	// exhausted stays true only if the loop ran out of iterations without the
	// strategy or a convergence rule having ended the run — that is a failure,
	// not a silent success.
	exhausted := true

	for i := 0; i < maxGroupIterations; i++ {
		// Check context cancellation.
		select {
		case <-ctx.Done():
			gm.finalize(mg, StatusStopped, nil)
			return
		default:
		}

		// Hard-stop check.
		gm.mu.Lock()
		stop, reason := StopReason(state)
		gm.mu.Unlock()
		if stop {
			log.Printf("group %s: stopped (%s)", state.ID, reason)
			exhausted = false
			break
		}

		// Ask the strategy who speaks next.
		speakers, done, err := strategy.Next(state)
		if err != nil {
			gm.finalize(mg, StatusError, err)
			return
		}
		if done {
			exhausted = false
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
					gm.finalize(mg, StatusStopped, nil)
					return
				}
				gm.finalize(mg, StatusError, err)
				return
			}
		} else {
			if err := gm.executeSequential(ctx, mg, speakers, layer); err != nil {
				if ctx.Err() != nil {
					gm.finalize(mg, StatusStopped, nil)
					return
				}
				gm.finalize(mg, StatusError, err)
				return
			}
		}

		// Re-check context after batch.
		select {
		case <-ctx.Done():
			gm.finalize(mg, StatusStopped, nil)
			return
		default:
		}
	}

	if exhausted {
		gm.finalize(mg, StatusError,
			fmt.Errorf("group %s: iterations exhausted after %d loops without convergence",
				state.ID, maxGroupIterations))
		return
	}

	// Normal completion (strategy said done, or a convergence hard-stop fired).
	// finalize computes the synthesis and publishes the terminal pair.
	gm.finalize(mg, StatusDone, nil)
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
		g.Go(func() (err error) {
			// A panic in a worker goroutine cannot be recovered by runGroup
			// (different goroutine) and would kill the process before any
			// terminal signal was emitted. Convert it into an error so the run
			// loop finalizes exactly once.
			defer func() {
				if r := recover(); r != nil {
					stack := debug.Stack()
					log.Printf("group %s: panic in parallel turn %s: %v\n%s", mg.state.ID, speaker, r, stack)
					err = fmt.Errorf("group panic in turn %s: %v\n%s", speaker, r, stack)
				}
			}()
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
		// An unresolvable speaker is a turn error, not a skip. Skipping would
		// leave the transcript untouched, so a strategy whose Next() is derived
		// from the transcript (round_robin, moa, moderator) would re-request the
		// same speaker on every iteration and burn the whole loop budget — up to
		// maxGroupIterations iterations producing a single turn — before failing.
		// Erroring here routes the group through finalize(StatusError) at once.
		log.Printf("group %s: speaker %q not resolvable", mg.state.ID, speaker)
		return fmt.Errorf("group %s: participant %q not resolvable", mg.state.ID, speaker)
	}

	inputs := gm.prepareTurn(mg, speaker, layer, agCtx)
	state := inputs.state
	self := inputs.self

	// Accumulate tool calls from OnToolCall callbacks during this turn.
	var tcMu sync.Mutex
	var toolCalls []GroupToolCall

	req := TurnRequest{
		GroupID:       state.ID,
		Speaker:       speaker,
		SystemPrompt:  inputs.sysPrompt,
		Transcript:    inputs.transcript,
		Instruction:   inputs.instruction,
		MaxTokens:     state.MaxTokensPerTurn,
		EnableTools:   true,
		OriginChannel: mg.originCh,
		OriginChatID:  mg.originChat,
		OnToolCall: func(toolCallID, toolName, args, status, result string) {
			gm.publish(bus.OutboundMessage{
				Channel:        mg.originCh,
				ChatID:         mg.originChat,
				Event:          "group.tool",
				IsIntermediate: true,
				Metadata: map[string]string{
					"group_id":     mg.state.ID,
					"speaker":      speaker,
					"label":        inputs.label,
					"layer":        strconv.Itoa(layer),
					"turn_index":   strconv.Itoa(inputs.turnIndex),
					"tool_call_id": toolCallID,
					"tool":         toolName,
					"status":       status,
					"arguments":    args,
					"result":       result,
				},
			})

			// Upsert the tool call into the accumulator.
			tcMu.Lock()
			found := false
			for i := range toolCalls {
				if toolCalls[i].ToolCallID == toolCallID {
					switch status {
					case "executing":
						if args != "" {
							toolCalls[i].Arguments = args
						}
					case "completed", "error":
						toolCalls[i].Status = status
						toolCalls[i].Result = result
						if toolCalls[i].Arguments == "" && args != "" {
							toolCalls[i].Arguments = args
						}
					}
					found = true
					break
				}
			}
			if !found {
				tc := GroupToolCall{
					ToolCallID: toolCallID,
					Tool:       toolName,
					Status:     status,
					Arguments:  args,
					Result:     result,
				}
				toolCalls = append(toolCalls, tc)
			}
			tcMu.Unlock()
		},
	}

	content, tokens, err := gm.executor(ctx, req)
	if err != nil {
		return fmt.Errorf("turn %s: %w", speaker, err)
	}

	turn, role := gm.recordTurn(state, self, speaker, layer, inputs.turnIndex, content, tokens, &tcMu, toolCalls)

	// Publish group.turn event.
	gm.publishTurn(mg, turn, role)

	return nil
}

// recordTurn appends a turn to the transcript under gm.mu and returns it with
// the speaker's role. The unlock is deferred so a panic while building the turn
// cannot leave the manager mutex held, which would deadlock finalize and break
// the terminal signal guarantee.
//
// turnIndex is the index reserved at turn start by prepareTurn. It is never
// recomputed here from len(Transcript): under Parallel execution the append
// order is the completion order, so a len-based index would collide between
// concurrent turns and would not match the turn_index already advertised to
// the client by group.tool events for this same turn. The turn keeps its
// reserved Index even though its position in Transcript may differ (see the
// comment on Turn.Index).
func (gm *GroupManager) recordTurn(state *GroupState, self Participant, speaker string, layer, turnIndex int, content string, tokens int, tcMu *sync.Mutex, toolCalls []GroupToolCall) (Turn, string) {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	turn := Turn{
		Index:     turnIndex,
		Layer:     layer,
		Speaker:   speaker,
		Label:     self.Label,
		Content:   content,
		CreatedAt: time.Now(),
		Tokens:    tokens,
	}
	tcMu.Lock()
	if len(toolCalls) > 0 {
		turn.ToolCalls = toolCalls
	}
	tcMu.Unlock()
	state.AddTurn(turn)
	return turn, self.Role
}

// turnInputs is the snapshot of per-turn request data computed under gm.mu.
type turnInputs struct {
	state       *GroupState
	self        Participant
	sysPrompt   string
	transcript  string
	instruction string
	turnIndex   int
	label       string
}

// prepareTurn reads the group state, reserves this turn's unique index, and
// renders the turn request inputs under gm.mu. The unlock is deferred so a
// panic inside the renderers cannot leave the manager mutex held (which would
// deadlock finalize and break the terminal signal guarantee).
//
// Index reservation (B5 fix): the index is taken from state.NextTurnIndex at
// turn START — before the speaker runs — instead of being derived from
// len(Transcript) when the turn is appended. Under Parallel execution N
// speakers run concurrently and append in completion order, so a
// len-at-append calculation could hand the same index to two turns (the
// transcript length only advances under the lock, but both turns read it
// before either appended) and the client-visible index would depend on timing.
// Reserving here guarantees every turn — and every group.tool event emitted
// while it runs — carries a single, unique, stable index regardless of
// completion order.
func (gm *GroupManager) prepareTurn(mg *managedGroup, speaker string, layer int, agCtx AgentContext) turnInputs {
	gm.mu.Lock()
	defer gm.mu.Unlock()

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

	// Reserve the index. Defensive re-base for states not built by Start (for
	// example a snapshot rehydrated by a future resume path where the counter
	// field is absent/zero but the transcript is not): the counter must never
	// collide with an index already present in the transcript.
	if state.NextTurnIndex < len(state.Transcript) {
		state.NextTurnIndex = len(state.Transcript)
	}
	idx := state.NextTurnIndex
	state.NextTurnIndex++

	return turnInputs{
		state:       state,
		self:        self,
		sysPrompt:   BuildTurnSystemPrompt(agCtx.SystemPrompt, self, state.Participants, state.Task),
		transcript:  RenderTranscript(state.Transcript),
		instruction: instructionFor(state, self, layer),
		turnIndex:   idx,
		label:       self.Label,
	}
}

// unsynthesizedPrefix marks a MoA result that no aggregator ever produced.
const unsynthesizedPrefix = "[unsynthesized: aggregator never spoke]\n"

// synthesisLocked computes the final synthesis for a group. It must be called
// with gm.mu held (it is only reached from finalize).
//
// For "moa" the synthesis is the aggregator's last turn. If the aggregator never
// spoke, the fallback below would otherwise return the last *proposer's* turn and
// present a raw proposal as if it were the synthesis. That is a mislabelled
// result, not an error worth killing an otherwise healthy run for, so the text is
// returned with an explicit marker instead of being hidden. GroupManager.Start now
// rejects a moderator outside participants, which removes the main way to reach
// this state; the marker stays as a safety net for states built elsewhere (for
// example a persisted group rehydrated with a Moderator that has since been
// removed from the registry).
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
		// The aggregator never spoke: flag the fallback rather than passing it
		// off as a synthesis.
		return unsynthesizedPrefix + state.Transcript[len(state.Transcript)-1].Content
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
