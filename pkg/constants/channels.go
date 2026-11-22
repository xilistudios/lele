package constants

var internalChannels = map[string]struct{}{
	"cli":      {},
	"system":   {},
	"subagent": {},
}

func IsInternalChannel(channel string) bool {
	_, found := internalChannels[channel]
	return found
}
