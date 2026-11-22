package group

import (
	"fmt"
	"sync"
)

type Strategy interface {
	Name() string
	Next(state *GroupState) (speakers []string, done bool, err error)
}

type StrategyFactory func() Strategy

var (
	strategyMu       sync.RWMutex
	strategyRegistry = make(map[string]StrategyFactory)
)

func RegisterStrategy(name string, f StrategyFactory) {
	strategyMu.Lock()
	defer strategyMu.Unlock()
	if _, exists := strategyRegistry[name]; exists {
		panic(fmt.Sprintf("group: strategy %q already registered", name))
	}
	strategyRegistry[name] = f
}

func UnregisterStrategy(name string) {
	strategyMu.Lock()
	defer strategyMu.Unlock()
	delete(strategyRegistry, name)
}

func NewStrategy(name string) (Strategy, error) {
	strategyMu.RLock()
	f, ok := strategyRegistry[name]
	strategyMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("group: unknown strategy %q", name)
	}
	return f(), nil
}
