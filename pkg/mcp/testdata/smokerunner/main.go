package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/xilistudios/lele/pkg/mcp"
	"github.com/xilistudios/lele/pkg/tools"
)

func fail(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "SMOKE FAILED: "+format+"\n", args...)
	os.Exit(1)
}

func main() {
	root := "/tmp/mcp-smoke"
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	serverBin := filepath.Join(root, "smokeserver")
	if len(os.Args) > 2 {
		serverBin = os.Args[2]
	}
	if _, err := os.Stat(serverBin); err != nil {
		fail("smokeserver binary %q not found (build it first): %v", serverBin, err)
	}

	workspace := filepath.Join(root, ".lele")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		fail("mkdir workspace: %v", err)
	}
	cfg := fmt.Sprintf(
		"{\"mcpServers\": {\"smoke\": {\"command\": %q, \"args\": [], \"description\": \"smoke\"}}}",
		serverBin,
	)
	if err := os.WriteFile(filepath.Join(workspace, "mcp.json"), []byte(cfg), 0o644); err != nil {
		fail("write mcp.json: %v", err)
	}

	paths := mcp.Paths{
		LeleDir:        filepath.Join(root, "lele-home"),
		AgentWorkspace: workspace,
		Cwd:            root,
	}
	mgr := mcp.NewManager(paths, nil)
	defer mgr.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	specs, err := mgr.LoadServer(ctx, "smoke")
	if err != nil {
		fail("LoadServer: %v", err)
	}
	if len(specs) != 1 || specs[0].RemoteName != "echo" {
		fail("specs = %+v, want exactly one echo tool", specs)
	}
	fmt.Printf("discovered: name=%s remote=%s\n", specs[0].Name, specs[0].RemoteName)

	factory := mgr.ToolFactory()
	var echo tools.Tool = factory(specs[0])
	if echo == nil {
		fail("ToolFactory returned nil for echo")
	}

	res := echo.Execute(ctx, map[string]interface{}{"message": "hola"})
	if res.IsError {
		fail("tool call errored: %s", res.ForLLM)
	}
	if res.ForLLM != "smoke:hola" {
		fail("result = %q, want %q", res.ForLLM, "smoke:hola")
	}
	fmt.Printf("round trip: %q\n", res.ForLLM)

	res = echo.Execute(ctx, map[string]interface{}{"message": "again"})
	if res.IsError || res.ForLLM != "smoke:again" {
		fail("second call = %q (err=%v), want smoke:again", res.ForLLM, res.IsError)
	}
	fmt.Println("SMOKE OK: real stdio child spawned, initialized and called twice")
}
