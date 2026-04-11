package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xilistudios/lele/pkg/i18n"
	"github.com/xilistudios/lele/pkg/skills"
)

func skillsHelp() {
	fmt.Println(i18n.T("cli.skills.help.title"))
	fmt.Println(i18n.T("cli.skills.help.list"))
	fmt.Println(i18n.T("cli.skills.help.install"))
	fmt.Println(i18n.T("cli.skills.help.installBuiltin"))
	fmt.Println(i18n.T("cli.skills.help.listBuiltin"))
	fmt.Println(i18n.T("cli.skills.help.remove"))
	fmt.Println(i18n.T("cli.skills.help.search"))
	fmt.Println(i18n.T("cli.skills.help.show"))
	fmt.Println()
	fmt.Println(i18n.T("cli.skills.help.examples"))
	fmt.Println(i18n.T("cli.skills.help.exampleList"))
	fmt.Println(i18n.T("cli.skills.help.exampleInstall"))
	fmt.Println(i18n.T("cli.skills.help.exampleInstallBuiltin"))
	fmt.Println(i18n.T("cli.skills.help.exampleListBuiltin"))
	fmt.Println(i18n.T("cli.skills.help.exampleRemove"))
}

func skillsListCmd(loader *skills.SkillsLoader) {
	allSkills := loader.ListSkills()

	if len(allSkills) == 0 {
		fmt.Println(i18n.T("cli.skills.noSkillsInstalled"))
		return
	}

	fmt.Println(i18n.T("cli.skills.installedSkills"))
	fmt.Println(i18n.T("cli.skills.installedSkillsSeparator"))
	for _, skill := range allSkills {
		fmt.Println(i18n.TPrintf("cli.skills.skillInstalled", skill.Name, skill.Source))
		if skill.Description != "" {
			fmt.Println(i18n.TPrintf("cli.skills.skillDescription", skill.Description))
		}
	}
}

func skillsInstallCmd(installer *skills.SkillInstaller) {
	if len(os.Args) < 4 {
		fmt.Println(i18n.T("cli.skills.usageInstall"))
		fmt.Println(i18n.T("cli.skills.exampleInstallUsage"))
		return
	}

	repo := os.Args[3]
	fmt.Println(i18n.TPrintf("cli.skills.installingSkill", repo))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := installer.InstallFromGitHub(ctx, repo); err != nil {
		fmt.Println(i18n.TPrintf("cli.skills.failedInstallSkill", err))
		os.Exit(1)
	}

	fmt.Println(i18n.TPrintf("cli.skills.skillInstalledSuccess", filepath.Base(repo)))
}

func skillsRemoveCmd(installer *skills.SkillInstaller, skillName string) {
	fmt.Println(i18n.TPrintf("cli.skills.removingSkill", skillName))

	if err := installer.Uninstall(skillName); err != nil {
		fmt.Println(i18n.TPrintf("cli.skills.failedRemoveSkill", err))
		os.Exit(1)
	}

	fmt.Println(i18n.TPrintf("cli.skills.skillRemovedSuccess", skillName))
}

func skillsInstallBuiltinCmd(workspace string) {
	builtinSkillsDir := "./lele/skills"
	workspaceSkillsDir := filepath.Join(workspace, "skills")

	fmt.Println(i18n.T("cli.skills.copyingBuiltinSkills"))

	skillsToInstall := []string{
		"weather",
		"news",
		"stock",
		"calculator",
	}

	for _, skillName := range skillsToInstall {
		builtinPath := filepath.Join(builtinSkillsDir, skillName)
		workspacePath := filepath.Join(workspaceSkillsDir, skillName)

		if _, err := os.Stat(builtinPath); err != nil {
			fmt.Println(i18n.TPrintf("cli.skills.builtinSkillNotFound", skillName, err))
			continue
		}

		if err := os.MkdirAll(workspacePath, 0755); err != nil {
			fmt.Println(i18n.TPrintf("cli.skills.failedCreateSkillDir", skillName, err))
			continue
		}

		if err := copyDirectory(builtinPath, workspacePath); err != nil {
			fmt.Println(i18n.TPrintf("cli.skills.failedCopySkill", skillName, err))
		}
	}

	fmt.Println(i18n.T("cli.skills.builtinSkillsInstalled"))
	fmt.Println(i18n.T("cli.skills.useInWorkspace"))
}

func skillsListBuiltinCmd() {
	cfg, err := loadConfig()
	if err != nil {
		fmt.Println(i18n.TPrintf("cli.common.errorLoadingConfig", err))
		return
	}
	builtinSkillsDir := filepath.Join(filepath.Dir(cfg.WorkspacePath()), "lele", "skills")

	fmt.Println(i18n.T("cli.skills.availableBuiltinSkills"))
	fmt.Println(i18n.T("cli.skills.builtinSkillsSeparator"))

	entries, err := os.ReadDir(builtinSkillsDir)
	if err != nil {
		fmt.Println(i18n.TPrintf("cli.skills.errorReadingBuiltinSkills", err))
		return
	}

	if len(entries) == 0 {
		fmt.Println(i18n.T("cli.skills.noBuiltinSkills"))
		return
	}

	for _, entry := range entries {
		if entry.IsDir() {
			skillName := entry.Name()
			skillFile := filepath.Join(builtinSkillsDir, skillName, "SKILL.md")

			description := "No description"
			if _, err := os.Stat(skillFile); err == nil {
				data, err := os.ReadFile(skillFile)
				if err == nil {
					content := string(data)
					if idx := strings.Index(content, "\n"); idx > 0 {
						firstLine := content[:idx]
						if strings.Contains(firstLine, "description:") {
							descLine := strings.Index(content[idx:], "\n")
							if descLine > 0 {
								description = strings.TrimSpace(content[idx+descLine : idx+descLine])
							}
						}
					}
				}
			}
			status := "✓"
			fmt.Println(i18n.TPrintf("cli.skills.skillPackage", status+"  "+entry.Name()))
			if description != "" {
				fmt.Println(i18n.TPrintf("cli.skills.skillDescription", description))
			}
		}
	}
}

func skillsSearchCmd(installer *skills.SkillInstaller) {
	fmt.Println(i18n.T("cli.skills.searchingSkills"))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	availableSkills, err := installer.ListAvailableSkills(ctx)
	if err != nil {
		fmt.Println(i18n.TPrintf("cli.skills.failedFetchSkillsList", err))
		return
	}

	if len(availableSkills) == 0 {
		fmt.Println(i18n.T("cli.skills.noSkillsAvailable"))
		return
	}

	fmt.Println(i18n.TPrintf("cli.skills.availableSkills", len(availableSkills)))
	fmt.Println(i18n.T("cli.skills.availableSkillsSeparator"))
	for _, skill := range availableSkills {
		fmt.Println(i18n.TPrintf("cli.skills.skillPackage", skill.Name))
		fmt.Println(i18n.TPrintf("cli.skills.skillDescription", skill.Description))
		fmt.Println(i18n.TPrintf("cli.skills.skillRepo", skill.Repository))
		if skill.Author != "" {
			fmt.Println(i18n.TPrintf("cli.skills.skillAuthor", skill.Author))
		}
		if len(skill.Tags) > 0 {
			fmt.Println(i18n.TPrintf("cli.skills.skillTags", skill.Tags))
		}
		fmt.Println()
	}
}

func skillsShowCmd(loader *skills.SkillsLoader, skillName string) {
	content, ok := loader.LoadSkill(skillName)
	if !ok {
		fmt.Println(i18n.TPrintf("cli.skills.skillNotFound", skillName))
		return
	}

	fmt.Println(i18n.TPrintf("cli.skills.skillShowTitle", skillName))
	fmt.Println(i18n.T("cli.skills.skillShowSeparator"))
	fmt.Println(content)
}
