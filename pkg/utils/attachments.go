package utils

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/logger"
)

const workspaceAttachmentsDir = "attachments"

func BuildAttachmentContext(attachments []bus.FileAttachment) string {
	if len(attachments) == 0 {
		return ""
	}

	var builder strings.Builder
	builder.WriteString("## Attachments\n")

	for idx, attachment := range attachments {
		path := strings.TrimSpace(attachment.Path)
		if path == "" {
			path = fmt.Sprintf("attachment-%d", idx+1)
		}
		builder.WriteString(fmt.Sprintf("- %s\n", path))
	}

	return strings.TrimSpace(builder.String())
}

func PersistAttachmentsToWorkspace(workspace string, attachments []bus.FileAttachment) ([]bus.FileAttachment, error) {
	if len(attachments) == 0 {
		return nil, nil
	}

	targetDir := filepath.Join(workspace, workspaceAttachmentsDir, time.Now().Format("20060102"))
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return nil, fmt.Errorf("create attachments directory: %w", err)
	}

	persisted := make([]bus.FileAttachment, 0, len(attachments))
	for _, attachment := range attachments {
		stored, err := persistAttachment(targetDir, attachment)
		if err != nil {
			return nil, err
		}
		persisted = append(persisted, stored)
	}

	return persisted, nil
}

func CleanupTempAttachments(attachments []bus.FileAttachment) {
	for _, attachment := range attachments {
		if !attachment.Temporary || attachment.Path == "" {
			continue
		}
		if err := os.Remove(attachment.Path); err != nil && !os.IsNotExist(err) {
			logger.DebugCF("attachments", "Failed to cleanup temp attachment", map[string]interface{}{
				"file":  attachment.Path,
				"error": err.Error(),
			})
		}
	}
}

func persistAttachment(targetDir string, attachment bus.FileAttachment) (bus.FileAttachment, error) {
	if attachment.Path == "" {
		return attachment, nil
	}

	fileName := attachment.Name
	if fileName == "" {
		fileName = filepath.Base(attachment.Path)
	}
	fileName = SanitizeFilename(fileName)
	if fileName == "." || fileName == "" {
		fileName = "attachment"
	}

	targetPath := filepath.Join(targetDir, uuid.New().String()[:8]+"_"+fileName)
	if sameFilePath(attachment.Path, targetPath) {
		attachment.Temporary = false
		return attachment, nil
	}

	if attachment.Temporary {
		if err := moveFile(attachment.Path, targetPath); err != nil {
			return bus.FileAttachment{}, fmt.Errorf("move attachment to workspace: %w", err)
		}
	} else {
		if err := copyFile(attachment.Path, targetPath); err != nil {
			return bus.FileAttachment{}, fmt.Errorf("copy attachment to workspace: %w", err)
		}
	}

	attachment.Path = targetPath
	attachment.Name = filepath.Base(targetPath)
	attachment.Temporary = false
	return attachment, nil
}

func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	if err := copyFile(src, dst); err != nil {
		return err
	}
	if err := os.Remove(src); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

func sameFilePath(a, b string) bool {
	cleanA := filepath.Clean(a)
	cleanB := filepath.Clean(b)
	return strings.EqualFold(cleanA, cleanB)
}

func CleanupOldUploads(uploadDir string, maxAge time.Duration) error {
	if uploadDir == "" {
		return nil
	}

	entries, err := os.ReadDir(uploadDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read upload directory: %w", err)
	}

	now := time.Now()
	cleanedCount := 0

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			logger.DebugCF("attachments", "Failed to get file info",
				map[string]interface{}{
					"file":  entry.Name(),
					"error": err.Error(),
				})
			continue
		}

		age := now.Sub(info.ModTime())
		if age > maxAge {
			path := filepath.Join(uploadDir, entry.Name())

			if err := os.Remove(path); err != nil {
				logger.WarnCF("attachments", "Failed to remove old upload",
					map[string]interface{}{
						"file":  path,
						"age":   age.String(),
						"error": err.Error(),
					})
				continue
			}

			cleanedCount++
			logger.DebugCF("attachments", "Removed old upload",
				map[string]interface{}{
					"file": path,
					"age":  age.String(),
				})
		}
	}

	if cleanedCount > 0 {
		logger.InfoCF("attachments", "Cleanup completed",
			map[string]interface{}{
				"cleaned": cleanedCount,
				"max_age": maxAge.String(),
			})
	}

	return nil
}
