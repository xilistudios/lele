//go:build !linux && !darwin && !windows

package keepawake

func platformStart() (func(), error) { return nil, ErrUnsupported }
