package version

import (
	"runtime/debug"
	"strings"
	"sync/atomic"
)

const unknown = "dev"

var injected atomic.Value

func Set(v string) {
	if v == "" || v == "(devel)" || v == unknown {
		return
	}
	injected.Store(v)
}

func Get() string {
	if v, ok := injected.Load().(string); ok && v != "" {
		return v
	}
	info, ok := debug.ReadBuildInfo()
	if !ok || info == nil || info.Main.Version == "" {
		return unknown
	}
	if info.Main.Version == "(devel)" {
		return unknown
	}
	return StripDirty(info.Main.Version)
}

func StripDirty(v string) string {
	for _, suffix := range []string{"+dirty", "-dirty"} {
		if strings.HasSuffix(v, suffix) {
			return strings.TrimSuffix(v, suffix)
		}
	}
	return v
}
