package mcp

import (
	"context"
	"errors"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
)

type ServerInfo struct {
	Name    string
	Version string
}

type ProbeResult struct {
	Tools  []string
	Server ServerInfo
}

type handshakeInfoer interface {
	handshakeInfo() ServerInfo
}

func ProbeServer(ctx context.Context, cfg ServerConfig) (ProbeResult, error) {
	return probeServer(ctx, cfg, nil)
}

func probeServer(ctx context.Context, cfg ServerConfig, dialer Dialer) (ProbeResult, error) {
	if err := cfg.validate(); err != nil {
		return ProbeResult{}, fmt.Errorf("mcp probe: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return ProbeResult{}, fmt.Errorf("mcp probe: %w", err)
	}
	if dialer == nil {
		dialer = newMCPDialer()
	}

	conn, err := dialer.Dial(ctx, cfg)
	if err != nil {
		return ProbeResult{}, fmt.Errorf("mcp probe: %w", err)
	}

	result := ProbeResult{}
	if info, ok := conn.(handshakeInfoer); ok {
		result.Server = info.handshakeInfo()
	}

	listed, listErr := conn.ListTools(ctx)
	closeErr := conn.Close()

	if listErr != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			listErr = ctxErr
		}
		if closeErr != nil {
			return ProbeResult{}, errors.Join(
				fmt.Errorf("mcp probe: %w", listErr),
				fmt.Errorf("mcp probe: close: %w", closeErr),
			)
		}
		return ProbeResult{}, fmt.Errorf("mcp probe: %w", listErr)
	}
	result.Tools = toolNames(listed)
	if closeErr != nil {
		return result, fmt.Errorf("mcp probe: close: %w", closeErr)
	}
	return result, nil
}

func toolNames(listed []mcp.Tool) []string {
	names := make([]string, 0, len(listed))
	for _, t := range listed {
		names = append(names, t.Name)
	}
	return names
}
