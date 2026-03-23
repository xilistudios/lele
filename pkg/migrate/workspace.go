package migrate

import (
	"os"
	"path/filepath"
)

type migrateableFile struct {
	Destination string
	Sources     []string
}

var migrateableFiles = []migrateableFile{
	{Destination: "AGENT.md", Sources: []string{"AGENT.md", "AGENTS.md"}},
	{Destination: "SOUL.md", Sources: []string{"SOUL.md"}},
	{Destination: "USER.md", Sources: []string{"USER.md"}},
	{Destination: "TOOLS.md", Sources: []string{"TOOLS.md"}},
	{Destination: "HEARTBEAT.md", Sources: []string{"HEARTBEAT.md"}},
}

var migrateableDirs = []string{
	"memory",
	"skills",
}

func PlanWorkspaceMigration(srcWorkspace, dstWorkspace string, force bool) ([]Action, error) {
	var actions []Action

	for _, file := range migrateableFiles {
		src := resolveMigrateableSource(srcWorkspace, file)
		dst := filepath.Join(dstWorkspace, file.Destination)
		action := planFileCopy(src, dst, force)
		if action.Type != ActionSkip || action.Description != "" {
			actions = append(actions, action)
		}
	}

	for _, dirname := range migrateableDirs {
		srcDir := filepath.Join(srcWorkspace, dirname)
		if _, err := os.Stat(srcDir); os.IsNotExist(err) {
			continue
		}
		dirActions, err := planDirCopy(srcDir, filepath.Join(dstWorkspace, dirname), force)
		if err != nil {
			return nil, err
		}
		actions = append(actions, dirActions...)
	}

	return actions, nil
}

func resolveMigrateableSource(srcWorkspace string, file migrateableFile) string {
	for _, name := range file.Sources {
		candidate := filepath.Join(srcWorkspace, name)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return filepath.Join(srcWorkspace, file.Destination)
}

func planFileCopy(src, dst string, force bool) Action {
	if _, err := os.Stat(src); os.IsNotExist(err) {
		return Action{
			Type:        ActionSkip,
			Source:      src,
			Destination: dst,
			Description: "source file not found",
		}
	}

	_, dstExists := os.Stat(dst)
	if dstExists == nil {
		return Action{
			Type:        ActionCopy,
			Source:      src,
			Destination: dst,
			Description: "destination exists, will overwrite",
		}
	}

	return Action{
		Type:        ActionCopy,
		Source:      src,
		Destination: dst,
		Description: "copy file",
	}
}

func planDirCopy(srcDir, dstDir string, force bool) ([]Action, error) {
	var actions []Action

	err := filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}

		dst := filepath.Join(dstDir, relPath)

		if info.IsDir() {
			actions = append(actions, Action{
				Type:        ActionCreateDir,
				Destination: dst,
				Description: "create directory",
			})
			return nil
		}

		action := planFileCopy(path, dst, force)
		actions = append(actions, action)
		return nil
	})

	return actions, err
}
