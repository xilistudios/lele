package agent

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/xilistudios/lele/pkg/skills"
	"github.com/xilistudios/lele/pkg/tools"
)

const maxRecordedConcurrencyFailures = 5

func TestSetWorkspace_RebuildsPromptAndStore(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	workspaceA := t.TempDir()
	workspaceB := t.TempDir()

	cb := NewContextBuilder(workspaceA)
	if !strings.Contains(cb.GetInitialContext(), workspaceA) {
		t.Fatalf("precondition: the prompt must render the initial workspace %q", workspaceA)
	}

	cb.SetWorkspace(workspaceB)

	prompt := cb.GetInitialContext()
	if !strings.Contains(prompt, workspaceB) {
		t.Errorf("prompt does not render the new workspace %q", workspaceB)
	}
	if strings.Contains(prompt, workspaceA) {
		t.Errorf("prompt still renders the old workspace %q", workspaceA)
	}
}

func TestSetWorkspace_NoOpWhenUnchanged(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	workspace := t.TempDir()

	cb := NewContextBuilder(workspace)
	if cb.GetInitialContext() == "" {
		t.Fatal("precondition: the prompt must be cached")
	}
	loader := cb.SkillsLoader()

	cb.SetWorkspace(workspace)

	if cb.SkillsLoader() != loader {
		t.Error("skills loader rebuilt for an unchanged workspace")
	}
	if cb.initialContext == "" {
		t.Error("cached prompt dropped for an unchanged workspace")
	}
}

func TestSetWorkspace_SkillsLoaderSeedsNewDir(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	workspaceA := t.TempDir()
	workspaceB := t.TempDir()
	writeTestSkill(t, workspaceA, "old-workspace-skill", "the old workspace skill")
	writeTestSkill(t, workspaceB, "new-workspace-skill", "the new workspace skill")

	cb := NewContextBuilder(workspaceA)
	if !strings.Contains(cb.GetInitialContext(), "old-workspace-skill") {
		t.Fatal("precondition: the prompt must list the initial workspace skill")
	}
	if _, ok := cb.SkillsLoader().LoadSkill("old-workspace-skill"); !ok {
		t.Fatal("precondition: the initial loader must resolve the initial workspace skill")
	}

	cb.SetWorkspace(workspaceB)

	if _, ok := cb.SkillsLoader().LoadSkill("new-workspace-skill"); !ok {
		t.Error("the rebuilt loader does not resolve a skill from the new workspace")
	}
	if _, ok := cb.SkillsLoader().LoadSkill("old-workspace-skill"); ok {
		t.Error("the rebuilt loader still resolves a skill from the old workspace")
	}
	prompt := cb.GetInitialContext()
	if !strings.Contains(prompt, "new-workspace-skill") {
		t.Error("prompt does not list the new workspace skill")
	}
	if strings.Contains(prompt, "old-workspace-skill") {
		t.Error("prompt still lists the old workspace skill")
	}
}

func TestSetWorkspace_KeepsEverySkillRoot(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", configDir)
	writeTestSkill(t, configDir, "global-skill", "the global skill body")
	workspaceA := t.TempDir()
	workspaceB := t.TempDir()
	writeTestSkill(t, workspaceA, "only-in-a", "a")
	writeTestSkill(t, workspaceB, "only-in-b", "b")

	cb := NewContextBuilder(workspaceA)
	if _, ok := cb.SkillsLoader().LoadSkill("global-skill"); !ok {
		t.Fatal("precondition: the global skill must resolve before the switch")
	}

	cb.SetWorkspace(workspaceB)
	loader := cb.SkillsLoader()

	if got, want := loader.GlobalSkillsDir(), filepath.Join(configDir, "skills"); got != want {
		t.Errorf("global skills root after the switch = %q, want %q", got, want)
	}
	if _, ok := loader.LoadSkill("global-skill"); !ok {
		t.Error("the rebuilt loader dropped the global skills root")
	}
	if _, ok := loader.LoadSkill("only-in-b"); !ok {
		t.Error("the rebuilt loader does not resolve a skill from the new workspace")
	}
	if _, ok := loader.LoadSkill("only-in-a"); ok {
		t.Error("the rebuilt loader still resolves a skill from the old workspace")
	}

	want := sortedSkillNames(NewContextBuilder(workspaceB).SkillsLoader())
	if got := sortedSkillNames(loader); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("rebuilt loader catalog = %v, want the freshly constructed catalog %v", got, want)
	}
}

func TestSetWorkspace_PreservesToolsCapabilitiesAndFilters(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	workspaceA := t.TempDir()
	workspaceB := t.TempDir()
	writeTestSkill(t, workspaceA, "kept-skill", "the kept skill body")
	writeTestSkill(t, workspaceB, "dropped-skill", "the filtered-out skill body")

	registry := tools.NewToolRegistry()
	registry.Register(tools.NewWriteFileTool(workspaceA, false))
	registry.Register(tools.NewReadImageTool(workspaceA, false))
	registry.Register(tools.NewReadVideoTool(workspaceA, false, tools.VideoCapabilities{}, nil))

	cb := NewContextBuilder(workspaceA)
	cb.SetToolsRegistry(registry)
	cb.SetVisionSupported(true)
	cb.SetVideoSupported(true)
	cb.SetSkillsFilter([]string{"kept-skill"})
	cb.SetAvailableSubagents([]subagentInfo{{ID: "scout", Description: "Recon"}})
	cb.SetMCPServers([]tools.MCPServerInfo{{Name: "files", Description: "local files"}})

	cb.SetWorkspace(workspaceB)

	prompt := cb.GetInitialContext()
	for _, want := range []string{
		"## Available Tools",
		"`write_file`",
		"`read_image`",
		"`read_video`",
		"## Subagents Available",
		"**scout**",
		"## MCP Servers",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt after the switch lost %q, so SetWorkspace reset workspace-independent state", want)
		}
	}
	if strings.Contains(prompt, "dropped-skill") {
		t.Error("prompt after the switch lists a skill the preserved filter excludes")
	}

	videoOnly := NewContextBuilder(workspaceA)
	videoRegistry := tools.NewToolRegistry()
	videoRegistry.Register(tools.NewReadVideoTool(workspaceA, false, tools.VideoCapabilities{}, nil))
	videoOnly.SetToolsRegistry(videoRegistry)
	videoOnly.SetVideoSupported(true)
	videoOnly.SetWorkspace(workspaceB)
	if prompt := videoOnly.GetInitialContext(); !strings.Contains(prompt, "`read_video`") {
		t.Error("prompt after the switch lost `read_video`, so SetWorkspace reset the video capability")
	}
}

func TestSetWorkspace_PinnedSkillsFollowTheCurrentLoader(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	workspaceA := t.TempDir()
	workspaceB := t.TempDir()
	writeTestSkill(t, workspaceA, "pinned-in-a", "the pinned body from A")

	cb := NewContextBuilder(workspaceA)
	if _, _, rejected := cb.PinSkills("s1", []string{"pinned-in-a"}); len(rejected) != 0 {
		t.Fatalf("precondition: the pin must be accepted: %v", rejected)
	}
	if prompt := cb.BuildSystemPromptForSession("s1", "agent"); !strings.Contains(prompt, "the pinned body from A") {
		t.Fatal("precondition: the pinned body must render before the switch")
	}

	cb.SetWorkspace(workspaceB)

	prompt := cb.BuildSystemPromptForSession("s1", "agent")
	if strings.Contains(prompt, "the pinned body from A") {
		t.Error("the prompt still renders a pinned body from the old workspace")
	}
	if strings.Contains(prompt, "Loaded Skills") {
		t.Error("the prompt advertises loaded skills while the current loader renders no body")
	}
	if names := cb.SkillsInContext("s1"); len(names) != 0 {
		t.Errorf("skills in context after the switch = %v, want none (the pinned skill lives in the old workspace)", names)
	}
}

func TestSetWorkspace_ConcurrentReadersStayConsistent(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	workspaceA := t.TempDir()
	workspaceB := t.TempDir()
	writeTestSkill(t, workspaceA, "skill-a", "the body of skill-a")
	writeTestSkill(t, workspaceB, "skill-b", "the body of skill-b")

	cb := NewContextBuilder(workspaceA)
	if _, _, rejected := cb.PinSkills("s1", []string{"skill-a"}); len(rejected) != 0 {
		t.Fatalf("precondition: the pin must be accepted: %v", rejected)
	}

	var (
		mu       sync.Mutex
		failures []string
		wg       sync.WaitGroup
	)
	fail := func(format string, args ...interface{}) {
		mu.Lock()
		defer mu.Unlock()
		if len(failures) < maxRecordedConcurrencyFailures {
			failures = append(failures, strings.TrimSpace(fmt.Sprintf(format, args...)))
		}
	}

	const swaps = 60
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < swaps; i++ {
			cb.SetWorkspace(workspaceB)
			cb.SetWorkspace(workspaceA)
		}
	}()

	readers := []func(){
		func() {
			names := skillNamesOf(cb.GetSkillsInfo())
			if !sameStringSet(names, []string{"skill-a"}) && !sameStringSet(names, []string{"skill-b"}) {
				fail("GetSkillsInfo enumerated a torn catalog: %v", names)
			}
		},
		func() {
			if prompt := cb.BuildSystemPromptForSession("s1", "agent"); strings.Contains(prompt, "Loaded Skills") &&
				!strings.Contains(prompt, "the body of skill-a") {
				fail("prompt advertises loaded skills without rendering a body from the same loader")
			}
		},
		func() {
			names := cb.SkillsInContext("s1")
			if !sameStringSet(names, []string{"skill-a"}) && !sameStringSet(names, []string{"skill-b"}) {
				fail("SkillsInContext mixed two loaders: %v", names)
			}
		},
		func() {
			loader := cb.SkillsLoader()
			if loader == nil {
				fail("SkillsLoader returned nil during a swap")
				return
			}
			if workspace := loader.WorkspaceDir(); workspace != workspaceA && workspace != workspaceB {
				fail("loader workspace = %q, want one of the two test workspaces", workspace)
			}
			if prompt := cb.GetInitialContext(); !strings.Contains(prompt, workspaceA) && !strings.Contains(prompt, workspaceB) {
				fail("prompt renders neither workspace during a swap")
			}
		},
	}
	for _, read := range readers {
		wg.Add(1)
		go func(read func()) {
			defer wg.Done()
			for i := 0; i < swaps; i++ {
				read()
			}
		}(read)
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	for _, failure := range failures {
		t.Error(failure)
	}
}
func sortedSkillNames(loader *skills.SkillsLoader) []string {
	infos := loader.ListSkills()
	names := make([]string, 0, len(infos))
	for _, info := range infos {
		names = append(names, info.Name)
	}
	sort.Strings(names)
	return names
}

func skillNamesOf(info map[string]interface{}) []string {
	names, _ := info["names"].([]string)
	return append([]string(nil), names...)
}
