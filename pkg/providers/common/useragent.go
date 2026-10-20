// Lele - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package common

import "github.com/xilistudios/lele/pkg/version"

// UserAgent identifies lele itself on outbound LLM requests.
//
// Some gateways (OpenCode Go among them) ask coding agents to identify
// themselves rather than appear as a generic HTTP library — which is exactly
// what Go's default "Go-http-client/1.1" looks like when a request carries no
// User-Agent.
func UserAgent() string { return "lele/" + version.Get() }
