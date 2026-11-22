package common

import "github.com/xilistudios/lele/pkg/version"

func UserAgent() string { return "lele/" + version.Get() }
