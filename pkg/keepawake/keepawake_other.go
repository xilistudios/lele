//go:build !linux && !darwin && !windows

package keepawake

// platformStart has no sleep-inhibition mechanism on this platform.
func platformStart() (func(), error) { return nil, ErrUnsupported }
