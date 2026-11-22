package agent

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

type MemoryStore struct {
	workspace  string
	memoryDir  string
	memoryFile string
}

func NewMemoryStore(workspace string) *MemoryStore {
	memoryDir := filepath.Join(workspace, "memory")
	memoryFile := filepath.Join(workspace, "MEMORY.md")

	if err := os.MkdirAll(memoryDir, 0755); err != nil {
		log.Printf("Warning: failed to create memory directory: %v", err)
	}

	return &MemoryStore{
		workspace:  workspace,
		memoryDir:  memoryDir,
		memoryFile: memoryFile,
	}
}

func (ms *MemoryStore) getTodayFile() string {
	today := time.Now().Format("20060102")
	monthDir := today[:6]
	filePath := filepath.Join(ms.memoryDir, monthDir, today+".md")
	return filePath
}

func (ms *MemoryStore) ReadLongTerm() string {
	if data, err := os.ReadFile(ms.memoryFile); err == nil {
		return string(data)
	}
	return ""
}

func (ms *MemoryStore) WriteLongTerm(content string) error {
	return os.WriteFile(ms.memoryFile, []byte(content), 0644)
}

func (ms *MemoryStore) ReadToday() string {
	todayFile := ms.getTodayFile()
	if data, err := os.ReadFile(todayFile); err == nil {
		return string(data)
	}
	return ""
}

func (ms *MemoryStore) AppendToday(content string) error {
	todayFile := ms.getTodayFile()

	monthDir := filepath.Dir(todayFile)
	if err := os.MkdirAll(monthDir, 0755); err != nil {
		return fmt.Errorf("failed to create month directory: %w", err)
	}

	var existingContent string
	if data, err := os.ReadFile(todayFile); err == nil {
		existingContent = string(data)
	}

	var newContent string
	if existingContent == "" {
		header := fmt.Sprintf("# %s\n\n", time.Now().Format("2006-01-02"))
		newContent = header + content
	} else {
		newContent = existingContent + "\n" + content
	}

	return os.WriteFile(todayFile, []byte(newContent), 0644)
}

func (ms *MemoryStore) GetRecentDailyNotes(days int) string {
	var notes []string

	for i := 0; i < days; i++ {
		date := time.Now().AddDate(0, 0, -i)
		dateStr := date.Format("20060102")
		monthDir := dateStr[:6]
		filePath := filepath.Join(ms.memoryDir, monthDir, dateStr+".md")

		if data, err := os.ReadFile(filePath); err == nil {
			notes = append(notes, string(data))
		}
	}

	if len(notes) == 0 {
		return ""
	}

	var result string
	for i, note := range notes {
		if i > 0 {
			result += "\n\n---\n\n"
		}
		result += note
	}
	return result
}

func (ms *MemoryStore) GetMemoryContext() string {
	longTerm := ms.ReadLongTerm()
	if longTerm == "" {
		return ""
	}

	return fmt.Sprintf("# Memory\n\n## Long-term Memory\n\n%s", longTerm)
}
