package main

import (
	"os"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/channels"
)

// --- skillsRemoveCmd error path ---
// skillsRemoveCmd calls os.Exit(1) on error, so the error branch cannot be
// exercised in-process. The success path is already covered in skills_test.go.

// --- clientRemoveCmd error path ---
// clientRemoveCmd calls os.Exit(1) on error; exercised only via success path.

// --- client status disabled branch (native disabled -> no server/connect) ---

func TestClientStatusCmd_Disabled(t *testing.T) {
	am := newAuthManager(t)
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	temp := 0.7
	cfg.Agents.Defaults.Temperature = &temp
	cfg.Channels.Native.Enabled = false
	out := runCmd(func() { clientStatusCmd(am, cfg) })
	if strings.Contains(out, "Server:") {
		t.Errorf("disabled native channel should not print server, got: %s", out)
	}
}

// defaultTestConfigUsed references channels to avoid unused import.
var _ = os.Getenv
var _ = channels.AuthManager{}