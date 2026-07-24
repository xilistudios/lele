package group

import (
	"fmt"
	"sync"
)

// Strategy is the interface that all group collaboration strategies must implement.
// A strategy determines who speaks next and when the group is done.
//
// Next returns the AgentIDs of the next speakers (more than one if turns are
// parallel within a layer), whether the group conversation has finished, and
// any error encountered during decision-making.
type Strategy interface {
	Name() string
	// Next returns the next speakers, whether the group is done, and any error.
	// speakers may contain multiple AgentIDs when the strategy supports parallel turns.
	Next(state *GroupState) (speakers []string, done bool, err error)
}

// StrategyFactory is a constructor function that creates a new Strategy instance.
type StrategyFactory func() Strategy

var (
	strategyMu       sync.RWMutex
	strategyRegistry = make(map[string]StrategyFactory)
)

// RegisterStrategy registers a strategy factory under the given name.
// It is safe to call from init() functions. Panics if the name is already registered.
func RegisterStrategy(name string, f StrategyFactory) {
	strategyMu.Lock()
	defer strategyMu.Unlock()
	if _, exists := strategyRegistry[name]; exists {
		panic(fmt.Sprintf("group: strategy %q already registered", name))
	}
	strategyRegistry[name] = f
}

// NewStrategy creates a new Strategy instance by looking up the factory registered
// under the given name. Returns an error if no strategy is registered with that name.
func NewStrategy(name string) (Strategy, error) {
	strategyMu.RLock()
	f, ok := strategyRegistry[name]
	strategyMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("group: unknown strategy %q", name)
	}
	return f(), nil
}
