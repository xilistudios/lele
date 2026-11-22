package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewMemoryStore_CreatesMemoryDir(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ms := NewMemoryStore(tempDir)

	memoryDir := filepath.Join(tempDir, "memory")
	if _, err := os.Stat(memoryDir); os.IsNotExist(err) {
		t.Error("Expected memory directory to be created")
	}

	if ms.workspace != tempDir {
		t.Errorf("Expected workspace to be '%s', got '%s'", tempDir, ms.workspace)
	}
	if ms.memoryDir != memoryDir {
		t.Errorf("Expected memoryDir to be '%s', got '%s'", memoryDir, ms.memoryDir)
	}
	expectedMemoryFile := filepath.Join(tempDir, "MEMORY.md")
	if ms.memoryFile != expectedMemoryFile {
		t.Errorf("Expected memoryFile to be '%s', got '%s'", expectedMemoryFile, ms.memoryFile)
	}
}

func TestNewMemoryStore_ExistingDir(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	memoryDir := filepath.Join(tempDir, "memory")
	if err := os.MkdirAll(memoryDir, 0755); err != nil {
		t.Fatalf("Failed to create memory dir: %v", err)
	}

	ms := NewMemoryStore(tempDir)

	if ms.workspace != tempDir {
		t.Errorf("Expected workspace to be '%s', got '%s'", tempDir, ms.workspace)
	}
}

func TestGetTodayFile_ReturnsCorrectPath(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ms := NewMemoryStore(tempDir)
	todayFile := ms.getTodayFile()

	today := time.Now().Format("20060102")
	monthDir := today[:6]

	expectedPath := filepath.Join(tempDir, "memory", monthDir, today+".md")
	if todayFile != expectedPath {
		t.Errorf("Expected path '%s', got '%s'", expectedPath, todayFile)
	}

	if !strings.Contains(todayFile, monthDir) {
		t.Error("Expected path to contain month directory (YYYYMM)")
	}
	if !strings.Contains(todayFile, today+".md") {
		t.Error("Expected path to contain date file (YYYYMMDD.md)")
	}
}

func TestReadLongTerm_ExistingFile(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ms := NewMemoryStore(tempDir)

	testContent := "# Long-term Memory\n\nThis is test content."
	if err := os.WriteFile(ms.memoryFile, []byte(testContent), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	result := ms.ReadLongTerm()
	if result != testContent {
		t.Errorf("Expected content '%s', got '%s'", testContent, result)
	}
}

func TestReadLongTerm_MissingFile(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ms := NewMemoryStore(tempDir)

	os.Remove(ms.memoryFile)

	result := ms.ReadLongTerm()
	if result != "" {
		t.Errorf("Expected empty string for missing file, got '%s'", result)
	}
}

func TestReadLongTerm_EmptyFile(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ms := NewMemoryStore(tempDir)

	if err := os.WriteFile(ms.memoryFile, []byte(""), 0644); err != nil {
		t.Fatalf("Failed to write empty file: %v", err)
	}

	result := ms.ReadLongTerm()
	if result != "" {
		t.Errorf("Expected empty string for empty file, got '%s'", result)
	}
}

func TestWriteLongTerm_CreatesFile(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ms := NewMemoryStore(tempDir)

	testContent := "# Test Memory\n\nThis is a test."
	err = ms.WriteLongTerm(testContent)
	if err != nil {
		t.Errorf("WriteLongTerm failed: %v", err)
	}

	data, err := os.ReadFile(ms.memoryFile)
	if err != nil {
		t.Fatalf("Failed to read written file: %v", err)
	}
	if string(data) != testContent {
		t.Errorf("Expected content '%s', got '%s'", testContent, string(data))
	}
}

func TestWriteLongTerm_OverwritesExisting(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ms := NewMemoryStore(tempDir)

	initialContent := "Initial content"
	if err := os.WriteFile(ms.memoryFile, []byte(initialContent), 0644); err != nil {
		t.Fatalf("Failed to write initial content: %v", err)
	}

	newContent := "New content"
	err = ms.WriteLongTerm(newContent)
	if err != nil {
		t.Errorf("WriteLongTerm failed: %v", err)
	}

	data, err := os.ReadFile(ms.memoryFile)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}
	if string(data) != newContent {
		t.Errorf("Expected content '%s', got '%s'", newContent, string(data))
	}
}

func TestWriteLongTerm_EmptyContent(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ms := NewMemoryStore(tempDir)

	err = ms.WriteLongTerm("")
	if err != nil {
		t.Errorf("WriteLongTerm with empty content failed: %v", err)
	}

	data, err := os.ReadFile(ms.memoryFile)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}
	if string(data) != "" {
		t.Errorf("Expected empty content, got '%s'", string(data))
	}
}

func TestReadToday_ExistingFile(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ms := NewMemoryStore(tempDir)

	todayFile := ms.getTodayFile()
	monthDir := filepath.Dir(todayFile)
	if err := os.MkdirAll(monthDir, 0755); err != nil {
		t.Fatalf("Failed to create month directory: %v", err)
	}

	testContent := "# Today's Notes\n\nTest note content."
	if err := os.WriteFile(todayFile, []byte(testContent), 0644); err != nil {
		t.Fatalf("Failed to write today's file: %v", err)
	}

	result := ms.ReadToday()
	if result != testContent {
		t.Errorf("Expected content '%s', got '%s'", testContent, result)
	}
}

func TestReadToday_MissingFile(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ms := NewMemoryStore(tempDir)

	result := ms.ReadToday()
	if result != "" {
		t.Errorf("Expected empty string for missing file, got '%s'", result)
	}
}

func TestReadToday_EmptyFile(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ms := NewMemoryStore(tempDir)

	todayFile := ms.getTodayFile()
	monthDir := filepath.Dir(todayFile)
	if err := os.MkdirAll(monthDir, 0755); err != nil {
		t.Fatalf("Failed to create month directory: %v", err)
	}
	if err := os.WriteFile(todayFile, []byte(""), 0644); err != nil {
		t.Fatalf("Failed to write empty file: %v", err)
	}

	result := ms.ReadToday()
	if result != "" {
		t.Errorf("Expected empty string for empty file, got '%s'", result)
	}
}

func TestAppendToday_NewFile(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ms := NewMemoryStore(tempDir)

	content := "First note entry"
	err = ms.AppendToday(content)
	if err != nil {
		t.Errorf("AppendToday failed: %v", err)
	}

	todayFile := ms.getTodayFile()
	data, err := os.ReadFile(todayFile)
	if err != nil {
		t.Fatalf("Failed to read today's file: %v", err)
	}

	result := string(data)
	if !strings.Contains(result, "# ") {
		t.Error("Expected file to contain date header")
	}
	if !strings.Contains(result, content) {
		t.Errorf("Expected file to contain '%s'", content)
	}
}

func TestAppendToday_ExistingFile(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ms := NewMemoryStore(tempDir)

	initialContent := "# 2026-03-08\n\nFirst entry"
	todayFile := ms.getTodayFile()
	monthDir := filepath.Dir(todayFile)
	if err := os.MkdirAll(monthDir, 0755); err != nil {
		t.Fatalf("Failed to create month directory: %v", err)
	}
	if err := os.WriteFile(todayFile, []byte(initialContent), 0644); err != nil {
		t.Fatalf("Failed to write initial content: %v", err)
	}

	newContent := "Second entry"
	err = ms.AppendToday(newContent)
	if err != nil {
		t.Errorf("AppendToday failed: %v", err)
	}

	data, err := os.ReadFile(todayFile)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}

	result := string(data)
	if !strings.Contains(result, "First entry") {
		t.Error("Expected file to still contain first entry")
	}
	if !strings.Contains(result, "Second entry") {
		t.Error("Expected file to contain second entry")
	}
	if !strings.Contains(result, "\nSecond entry") {
		t.Error("Expected newline separator before second entry")
	}
}

func TestAppendToday_CreatesMonthDir(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ms := NewMemoryStore(tempDir)

	todayFile := ms.getTodayFile()
	monthDir := filepath.Dir(todayFile)
	os.RemoveAll(monthDir)

	err = ms.AppendToday("Test content")
	if err != nil {
		t.Errorf("AppendToday failed: %v", err)
	}

	if _, err := os.Stat(monthDir); os.IsNotExist(err) {
		t.Error("Expected month directory to be created")
	}
}

func TestAppendToday_MultipleAppends(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ms := NewMemoryStore(tempDir)

	entries := []string{"Entry 1", "Entry 2", "Entry 3"}
	for _, entry := range entries {
		if err := ms.AppendToday(entry); err != nil {
			t.Errorf("AppendToday failed: %v", err)
		}
	}

	todayFile := ms.getTodayFile()
	data, err := os.ReadFile(todayFile)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}

	result := string(data)
	for _, entry := range entries {
		if !strings.Contains(result, entry) {
			t.Errorf("Expected file to contain '%s'", entry)
		}
	}
}

func TestGetRecentDailyNotes_NoFiles(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ms := NewMemoryStore(tempDir)

	result := ms.GetRecentDailyNotes(3)
	if result != "" {
		t.Errorf("Expected empty string when no notes exist, got '%s'", result)
	}
}

func TestGetRecentDailyNotes_TodayOnly(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ms := NewMemoryStore(tempDir)

	today := time.Now().Format("20060102")
	monthDir := today[:6]
	todayFile := filepath.Join(ms.memoryDir, monthDir, today+".md")
	if err := os.MkdirAll(filepath.Dir(todayFile), 0755); err != nil {
		t.Fatalf("Failed to create month directory: %v", err)
	}
	todayContent := "# Today\n\nToday's note"
	if err := os.WriteFile(todayFile, []byte(todayContent), 0644); err != nil {
		t.Fatalf("Failed to write today's file: %v", err)
	}

	result := ms.GetRecentDailyNotes(3)
	if !strings.Contains(result, "Today's note") {
		t.Error("Expected result to contain today's note")
	}
}

func TestGetRecentDailyNotes_MultipleDays(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ms := NewMemoryStore(tempDir)

	for i := 0; i < 3; i++ {
		date := time.Now().AddDate(0, 0, -i)
		dateStr := date.Format("20060102")
		monthDir := dateStr[:6]
		filePath := filepath.Join(ms.memoryDir, monthDir, dateStr+".md")
		if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
			t.Fatalf("Failed to create month directory: %v", err)
		}
		content := fmt.Sprintf("Note from day %d", i)
		if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
			t.Fatalf("Failed to write file: %v", err)
		}
	}

	result := ms.GetRecentDailyNotes(3)

	if !strings.Contains(result, "Note from day 0") {
		t.Error("Expected result to contain today's note")
	}
	if !strings.Contains(result, "Note from day 1") {
		t.Error("Expected result to contain yesterday's note")
	}
	if !strings.Contains(result, "Note from day 2") {
		t.Error("Expected result to contain note from 2 days ago")
	}

	if !strings.Contains(result, "---") {
		t.Error("Expected result to contain separators between notes")
	}
}

func TestGetRecentDailyNotes_PartialDays(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ms := NewMemoryStore(tempDir)

	today := time.Now().Format("20060102")
	todayMonthDir := today[:6]
	todayFile := filepath.Join(ms.memoryDir, todayMonthDir, today+".md")
	if err := os.MkdirAll(filepath.Dir(todayFile), 0755); err != nil {
		t.Fatalf("Failed to create month directory: %v", err)
	}
	if err := os.WriteFile(todayFile, []byte("Today note"), 0644); err != nil {
		t.Fatalf("Failed to write today's file: %v", err)
	}

	twoDaysAgo := time.Now().AddDate(0, 0, -2).Format("20060102")
	twoDaysAgoMonthDir := twoDaysAgo[:6]
	twoDaysAgoFile := filepath.Join(ms.memoryDir, twoDaysAgoMonthDir, twoDaysAgo+".md")
	if err := os.MkdirAll(filepath.Dir(twoDaysAgoFile), 0755); err != nil {
		t.Fatalf("Failed to create month directory: %v", err)
	}
	if err := os.WriteFile(twoDaysAgoFile, []byte("Two days ago note"), 0644); err != nil {
		t.Fatalf("Failed to write file: %v", err)
	}

	result := ms.GetRecentDailyNotes(3)

	if !strings.Contains(result, "Today note") {
		t.Error("Expected result to contain today's note")
	}
	if !strings.Contains(result, "Two days ago note") {
		t.Error("Expected result to contain 2 days ago note")
	}
}

func TestGetRecentDailyNotes_ZeroDays(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ms := NewMemoryStore(tempDir)

	today := time.Now().Format("20060102")
	monthDir := today[:6]
	todayFile := filepath.Join(ms.memoryDir, monthDir, today+".md")
	if err := os.MkdirAll(filepath.Dir(todayFile), 0755); err != nil {
		t.Fatalf("Failed to create month directory: %v", err)
	}
	if err := os.WriteFile(todayFile, []byte("Today note"), 0644); err != nil {
		t.Fatalf("Failed to write file: %v", err)
	}

	result := ms.GetRecentDailyNotes(0)
	if result != "" {
		t.Errorf("Expected empty string for 0 days, got '%s'", result)
	}
}

func TestGetRecentDailyNotes_CrossMonth(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ms := NewMemoryStore(tempDir)

	now := time.Now()
	firstOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	lastDayOfPrevMonth := firstOfMonth.AddDate(0, 0, -1)

	prevMonthStr := lastDayOfPrevMonth.Format("20060102")
	prevMonthDir := prevMonthStr[:6]
	prevMonthFile := filepath.Join(ms.memoryDir, prevMonthDir, prevMonthStr+".md")
	if err := os.MkdirAll(filepath.Dir(prevMonthFile), 0755); err != nil {
		t.Fatalf("Failed to create month directory: %v", err)
	}
	if err := os.WriteFile(prevMonthFile, []byte("Previous month note"), 0644); err != nil {
		t.Fatalf("Failed to write file: %v", err)
	}

	currMonthStr := firstOfMonth.Format("20060102")
	currMonthDir := currMonthStr[:6]
	currMonthFile := filepath.Join(ms.memoryDir, currMonthDir, currMonthStr+".md")
	if err := os.MkdirAll(filepath.Dir(currMonthFile), 0755); err != nil {
		t.Fatalf("Failed to create month directory: %v", err)
	}
	if err := os.WriteFile(currMonthFile, []byte("Current month note"), 0644); err != nil {
		t.Fatalf("Failed to write file: %v", err)
	}

	daysDiff := int(now.Sub(lastDayOfPrevMonth).Hours()/24) + 1
	result := ms.GetRecentDailyNotes(daysDiff)

	if !strings.Contains(result, "Previous month note") {
		t.Error("Expected result to contain previous month note")
	}
	if !strings.Contains(result, "Current month note") {
		t.Error("Expected result to contain current month note")
	}
}

func TestGetMemoryContext_NoMemory(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ms := NewMemoryStore(tempDir)

	result := ms.GetMemoryContext()
	if result != "" {
		t.Errorf("Expected empty string when no memory exists, got '%s'", result)
	}
}

func TestGetMemoryContext_OnlyLongTerm(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ms := NewMemoryStore(tempDir)

	longTermContent := "Important long-term information"
	if err := ms.WriteLongTerm(longTermContent); err != nil {
		t.Fatalf("Failed to write long-term memory: %v", err)
	}

	result := ms.GetMemoryContext()

	if !strings.Contains(result, "# Memory") {
		t.Error("Expected result to contain '# Memory' header")
	}
	if !strings.Contains(result, "## Long-term Memory") {
		t.Error("Expected result to contain long-term memory section")
	}
	if !strings.Contains(result, longTermContent) {
		t.Error("Expected result to contain long-term content")
	}
	if strings.Contains(result, "## Recent Daily Notes") {
		t.Error("Expected result to not contain daily notes section when none exist")
	}
}

func TestGetMemoryContext_OnlyDailyNotes(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ms := NewMemoryStore(tempDir)

	if err := ms.AppendToday("Today's activity"); err != nil {
		t.Fatalf("Failed to append today's note: %v", err)
	}

	result := ms.GetMemoryContext()

	if result != "" {
		t.Errorf("Expected empty result when only daily notes exist, got: %s", result)
	}
}

func TestGetMemoryContext_BothMemories(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ms := NewMemoryStore(tempDir)

	longTermContent := "Important facts"
	if err := ms.WriteLongTerm(longTermContent); err != nil {
		t.Fatalf("Failed to write long-term memory: %v", err)
	}

	if err := ms.AppendToday("Daily activity"); err != nil {
		t.Fatalf("Failed to append today's note: %v", err)
	}

	result := ms.GetMemoryContext()

	if !strings.Contains(result, "## Long-term Memory") {
		t.Error("Expected result to contain long-term memory section")
	}
	if !strings.Contains(result, longTermContent) {
		t.Error("Expected result to contain long-term content")
	}
	if strings.Contains(result, "## Recent Daily Notes") {
		t.Error("Expected result to NOT contain daily notes section")
	}
	if strings.Contains(result, "Daily activity") {
		t.Error("Expected result to NOT contain daily note content")
	}
}

func TestGetMemoryContext_EmptyLongTerm(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ms := NewMemoryStore(tempDir)

	if err := ms.WriteLongTerm(""); err != nil {
		t.Fatalf("Failed to write empty long-term memory: %v", err)
	}

	if err := ms.AppendToday("Daily activity"); err != nil {
		t.Fatalf("Failed to append today's note: %v", err)
	}

	result := ms.GetMemoryContext()

	if strings.Contains(result, "## Long-term Memory") {
		t.Error("Expected result to not contain long-term section when empty")
	}
	if strings.Contains(result, "## Recent Daily Notes") {
		t.Error("Expected result to NOT contain daily notes section")
	}
	if result != "" {
		t.Errorf("Expected empty result, got: %s", result)
	}
}

func TestMemoryStore_FullWorkflow(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ms := NewMemoryStore(tempDir)

	longTermContent := "# User Preferences\n\n- Name: John\n- Language: English"
	if err := ms.WriteLongTerm(longTermContent); err != nil {
		t.Fatalf("Failed to write long-term memory: %v", err)
	}

	readLongTerm := ms.ReadLongTerm()
	if readLongTerm != longTermContent {
		t.Error("Long-term memory read/write mismatch")
	}

	if err := ms.AppendToday("Started working on project"); err != nil {
		t.Fatalf("Failed to append to today's notes: %v", err)
	}

	todayNotes := ms.ReadToday()
	if !strings.Contains(todayNotes, "Started working on project") {
		t.Error("Today's notes don't contain expected content")
	}

	if err := ms.AppendToday("Made good progress"); err != nil {
		t.Fatalf("Failed to append to today's notes: %v", err)
	}

	context := ms.GetMemoryContext()
	if !strings.Contains(context, "User Preferences") {
		t.Error("Memory context doesn't contain long-term memory")
	}
	if strings.Contains(context, "Started working on project") {
		t.Error("Memory context should NOT contain daily notes anymore")
	}
	if strings.Contains(context, "Made good progress") {
		t.Error("Memory context should NOT contain daily notes anymore")
	}

	recentNotes := ms.GetRecentDailyNotes(1)
	if !strings.Contains(recentNotes, "Started working on project") {
		t.Error("Recent notes don't contain expected content")
	}
}

func TestMemoryStore_ConcurrentAccess(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ms := NewMemoryStore(tempDir)

	done := make(chan bool, 3)

	go func() {
		for i := 0; i < 10; i++ {
			ms.WriteLongTerm("Content " + string(rune('A'+i)))
		}
		done <- true
	}()

	go func() {
		for i := 0; i < 10; i++ {
			ms.AppendToday("Note " + string(rune('0'+i%10)))
		}
		done <- true
	}()

	go func() {
		for i := 0; i < 10; i++ {
			_ = ms.ReadLongTerm()
			_ = ms.ReadToday()
			_ = ms.GetMemoryContext()
		}
		done <- true
	}()

	for i := 0; i < 3; i++ {
		<-done
	}

	if _, err := os.Stat(ms.memoryFile); os.IsNotExist(err) {
		t.Error("Long-term memory file should exist after concurrent access")
	}

	todayFile := ms.getTodayFile()
	if _, err := os.Stat(todayFile); os.IsNotExist(err) {
		t.Error("Today's file should exist after concurrent access")
	}
}
