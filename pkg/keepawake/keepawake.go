package keepawake

import (
	"errors"
	"sync"
)

var ErrUnsupported = errors.New("keep-awake is not supported on this platform")

type Inhibitor struct {
	mu      sync.Mutex
	starter func() (release func(), err error)
	release func()
}

func New() *Inhibitor {
	return NewWithStarter(platformStart)
}

func NewWithStarter(starter func() (func(), error)) *Inhibitor {
	return &Inhibitor{starter: starter}
}

func (i *Inhibitor) Start() error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.release != nil {
		return nil
	}
	starter := i.starter
	if starter == nil {
		starter = platformStart
	}
	release, err := starter()
	if err != nil {
		if release != nil {
			release()
		}
		return err
	}
	if release == nil {
		return errors.New("keep-awake: starter returned no release func")
	}
	i.release = release
	return nil
}

func (i *Inhibitor) Stop() {
	i.mu.Lock()
	release := i.release
	i.release = nil
	i.mu.Unlock()
	if release != nil {
		release()
	}
}

func (i *Inhibitor) Active() bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.release != nil
}
