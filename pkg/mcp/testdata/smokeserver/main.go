// Command smokeserver is a minimal MCP stdio server used by the manual T12
// smoke documented in docs/mcp.md ("Manual smoke"). It exposes a single
// "echo" tool that answers "smoke:<message>", which is what the smoke
// asserts end to end through lele's production dialer.
//
// It lives under testdata so `go build ./...`, vet and CI never pick it up.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func main() {
	srv := server.NewMCPServer("lele-smoketest", "0.1.0",
		server.WithToolCapabilities(false))

	echo := mcp.NewTool("echo",
		mcp.WithString("message",
			mcp.Required(),
			mcp.Description("Text to echo back prefixed with 'smoke:'.")),
	)
	srv.AddTool(echo, func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		msg, err := req.RequireString("message")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText("smoke:" + msg), nil
	})

	if err := server.ServeStdio(srv); err != nil {
		fmt.Fprintf(os.Stderr, "smokeserver: %v\n", err)
		os.Exit(1)
	}
}
