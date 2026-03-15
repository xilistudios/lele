// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ============================================================================
// Tests for NewMemoryStore
// ============================================================================

// TestNewMemoryStore_CreatesMemoryDir tests that NewMemoryStore creates the memory directory
func TestNewMemoryStore_CreatesMemoryDir(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ms := NewMemoryStore(tempDir)

	// Verify memory directory was created
	memoryDir := filepath.Join(tempDir, "memory")
	if _, err := os.Stat(memoryDir); os.IsNotExist(err) {
		t.Error("Expected memory directory to be created")
	}

	// Verify MemoryStore fields are set correctly
	if ms.workspace != tempDir {
		t.Errorf("Expected workspace to be '%s', got '%s'", tempDir, ms.workspace)
	}
	if ms.memoryDir != memoryDir {
		t.Errorf("Expected memoryDir to be '%s', got '%s'", memoryDir, ms.memoryDir)
	}
	expectedMemoryFile := filepath.Join(memoryDir, "MEMORY.md")
	if ms.memoryFile != expectedMemoryFile {
		t.Errorf("Expected memoryFile to be '%s', got '%s'", expectedMemoryFile, ms.memoryFile)
	}
}

// TestNewMemoryStore_ExistingDir tests that NewMemoryStore works with existing memory directory
func TestNewMemoryStore_ExistingDir(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Pre-create the memory directory
	memoryDir := filepath.Join(tempDir, "memory")
	if err := os.MkdirAll(memoryDir, 0755); err != nil {
		t.Fatalf("Failed to create memory dir: %v", err)
	}

	// Should not fail when directory already exists
	ms := NewMemoryStore(tempDir)

	if ms.workspace != tempDir {
		t.Errorf("Expected workspace to be '%s', got '%s'", tempDir, ms.workspace)
	}
}

// ============================================================================
// Tests for getTodayFile
// ============================================================================

// TestGetTodayFile_ReturnsCorrectPath tests that getTodayFile returns the correct path format
func TestGetTodayFile_ReturnsCorrectPath(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ms := NewMemoryStore(tempDir)
	todayFile := ms.getTodayFile()

	// Get today's date
	today := time.Now().Format("20060102")
	monthDir := today[:6]

	// Verify path format: memory/YYYYMM/YYYYMMDD.md
	expectedPath := filepath.Join(tempDir, "memory", monthDir, today+".md")
	if todayFile != expectedPath {
		t.Errorf("Expected path '%s', got '%s'", expectedPath, todayFile)
	}

	// Verify the path contains the correct date format
	if !strings.Contains(todayFile, monthDir) {
		t.Error("Expected path to contain month directory (YYYYMM)")
	}
	if !strings.Contains(todayFile, today+".md") {
		t.Error("Expected path to contain date file (YYYYMMDD.md)")
	}
}

// ============================================================================
// Tests for ReadLongTerm
// ============================================================================

// TestReadLongTerm_ExistingFile tests reading from an existing MEMORY.md file
func TestReadLongTerm_ExistingFile(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ms := NewMemoryStore(tempDir)

	// Create the memory file with content
	testContent := "# Long-term Memory\n\nThis is test content."
	if err := os.WriteFile(ms.memoryFile, []byte(testContent), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	result := ms.ReadLongTerm()
	if result != testContent {
		t.Errorf("Expected content '%s', got '%s'", testContent, result)
	}
}

// TestReadLongTerm_MissingFile tests reading when MEMORY.md doesn't exist
func TestReadLongTerm_MissingFile(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ms := NewMemoryStore(tempDir)

	// Ensure file doesn't exist
	os.Remove(ms.memoryFile)

	result := ms.ReadLongTerm()
	if result != "" {
		t.Errorf("Expected empty string for missing file, got '%s'", result)
	}
}

// TestReadLongTerm_EmptyFile tests reading from an empty MEMORY.md file
func TestReadLongTerm_EmptyFile(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ms := NewMemoryStore(tempDir)

	// Create empty file
	if err := os.WriteFile(ms.memoryFile, []byte(""), 0644); err != nil {
		t.Fatalf("Failed to write empty file: %v", err)
	}

	result := ms.ReadLongTerm()
	if result != "" {
		t.Errorf("Expected empty string for empty file, got '%s'", result)
	}
}

// ============================================================================
// Tests for WriteLongTerm
// ============================================================================

// TestWriteLongTerm_CreatesFile tests writing long-term memory creates the file
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

	// Verify file was created and contains content
	data, err := os.ReadFile(ms.memoryFile)
	if err != nil {
		t.Fatalf("Failed to read written file: %v", err)
	}
	if string(data) != testContent {
		t.Errorf("Expected content '%s', got '%s'", testContent, string(data))
	}
}

// TestWriteLongTerm_OverwritesExisting tests that WriteLongTerm overwrites existing content
func TestWriteLongTerm_OverwritesExisting(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ms := NewMemoryStore(tempDir)

	// Write initial content
	initialContent := "Initial content"
	if err := os.WriteFile(ms.memoryFile, []byte(initialContent), 0644); err != nil {
		t.Fatalf("Failed to write initial content: %v", err)
	}

	// Overwrite with new content
	newContent := "New content"
	err = ms.WriteLongTerm(newContent)
	if err != nil {
		t.Errorf("WriteLongTerm failed: %v", err)
	}

	// Verify content was overwritten
	data, err := os.ReadFile(ms.memoryFile)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}
	if string(data) != newContent {
		t.Errorf("Expected content '%s', got '%s'", newContent, string(data))
	}
}

// TestWriteLongTerm_EmptyContent tests writing empty content
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

	// Verify file exists and is empty
	data, err := os.ReadFile(ms.memoryFile)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}
	if string(data) != "" {
		t.Errorf("Expected empty content, got '%s'", string(data))
	}
}

// ============================================================================
// Tests for ReadToday
// ============================================================================

// TestReadToday_ExistingFile tests reading today's note when file exists
func TestReadToday_ExistingFile(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ms := NewMemoryStore(tempDir)

	// Create today's file
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

// TestReadToday_MissingFile tests reading today's note when file doesn't exist
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

// TestReadToday_EmptyFile tests reading from an empty today's file
func TestReadToday_EmptyFile(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ms := NewMemoryStore(tempDir)

	// Create empty today's file
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

// ============================================================================
// Tests for AppendToday
// ============================================================================

// TestAppendToday_NewFile tests appending to a new daily note file
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

	// Verify file was created with header
	todayFile := ms.getTodayFile()
	data, err := os.ReadFile(todayFile)
	if err != nil {
		t.Fatalf("Failed to read today's file: %v", err)
	}

	// Should contain date header and content
	result := string(data)
	if !strings.Contains(result, "# ") {
		t.Error("Expected file to contain date header")
	}
	if !strings.Contains(result, content) {
		t.Errorf("Expected file to contain '%s'", content)
	}
}

// TestAppendToday_ExistingFile tests appending to an existing daily note file
func TestAppendToday_ExistingFile(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ms := NewMemoryStore(tempDir)

	// Create initial content
	initialContent := "# 2026-03-08\n\nFirst entry"
	todayFile := ms.getTodayFile()
	monthDir := filepath.Dir(todayFile)
	if err := os.MkdirAll(monthDir, 0755); err != nil {
		t.Fatalf("Failed to create month directory: %v", err)
	}
	if err := os.WriteFile(todayFile, []byte(initialContent), 0644); err != nil {
		t.Fatalf("Failed to write initial content: %v", err)
	}

	// Append new content
	newContent := "Second entry"
	err = ms.AppendToday(newContent)
	if err != nil {
		t.Errorf("AppendToday failed: %v", err)
	}

	// Verify content was appended
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
	// Should have newline separator
	if !strings.Contains(result, "\nSecond entry") {
		t.Error("Expected newline separator before second entry")
	}
}

// TestAppendToday_CreatesMonthDir tests that AppendToday creates the month directory
func TestAppendToday_CreatesMonthDir(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ms := NewMemoryStore(tempDir)

	// Ensure month directory doesn't exist yet
	todayFile := ms.getTodayFile()
	monthDir := filepath.Dir(todayFile)
	os.RemoveAll(monthDir)

	err = ms.AppendToday("Test content")
	if err != nil {
		t.Errorf("AppendToday failed: %v", err)
	}

	// Verify month directory was created
	if _, err := os.Stat(monthDir); os.IsNotExist(err) {
		t.Error("Expected month directory to be created")
	}
}

// TestAppendToday_MultipleAppends tests multiple appends to the same day
func TestAppendToday_MultipleAppends(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ms := NewMemoryStore(tempDir)

	// Append multiple times
	entries := []string{"Entry 1", "Entry 2", "Entry 3"}
	for _, entry := range entries {
		if err := ms.AppendToday(entry); err != nil {
			t.Errorf("AppendToday failed: %v", err)
		}
	}

	// Verify all entries are present
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

// ============================================================================
// Tests for GetRecentDailyNotes
// ============================================================================

// TestGetRecentDailyNotes_NoFiles tests when no daily notes exist
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

// TestGetRecentDailyNotes_TodayOnly tests with only today's note
func TestGetRecentDailyNotes_TodayOnly(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ms := NewMemoryStore(tempDir)

	// Create today's note
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

// TestGetRecentDailyNotes_MultipleDays tests with multiple days of notes
func TestGetRecentDailyNotes_MultipleDays(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ms := NewMemoryStore(tempDir)

	// Create notes for today, yesterday, and 2 days ago
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

	// Should contain all three notes
	if !strings.Contains(result, "Note from day 0") {
		t.Error("Expected result to contain today's note")
	}
	if !strings.Contains(result, "Note from day 1") {
		t.Error("Expected result to contain yesterday's note")
	}
	if !strings.Contains(result, "Note from day 2") {
		t.Error("Expected result to contain note from 2 days ago")
	}

	// Should have separators between notes
	if !strings.Contains(result, "---") {
		t.Error("Expected result to contain separators between notes")
	}
}

// TestGetRecentDailyNotes_PartialDays tests when some days have notes and some don't
func TestGetRecentDailyNotes_PartialDays(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ms := NewMemoryStore(tempDir)

	// Create note only for today and 2 days ago (skip yesterday)
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

	// Should contain today's note
	if !strings.Contains(result, "Today note") {
		t.Error("Expected result to contain today's note")
	}
	// Should contain 2 days ago note
	if !strings.Contains(result, "Two days ago note") {
		t.Error("Expected result to contain 2 days ago note")
	}
}

// TestGetRecentDailyNotes_ZeroDays tests with days=0
func TestGetRecentDailyNotes_ZeroDays(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ms := NewMemoryStore(tempDir)

	// Create a note
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

// TestGetRecentDailyNotes_CrossMonth tests notes that cross month boundaries
func TestGetRecentDailyNotes_CrossMonth(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ms := NewMemoryStore(tempDir)

	// Create notes in different months (simulate by using specific dates)
	// Note: This test may need adjustment based on current date
	// We'll create files for specific dates to test cross-month functionality

	// Create a note for the first day of current month
	now := time.Now()
	firstOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	lastDayOfPrevMonth := firstOfMonth.AddDate(0, 0, -1)

	// Create note for last day of previous month
	prevMonthStr := lastDayOfPrevMonth.Format("20060102")
	prevMonthDir := prevMonthStr[:6]
	prevMonthFile := filepath.Join(ms.memoryDir, prevMonthDir, prevMonthStr+".md")
	if err := os.MkdirAll(filepath.Dir(prevMonthFile), 0755); err != nil {
		t.Fatalf("Failed to create month directory: %v", err)
	}
	if err := os.WriteFile(prevMonthFile, []byte("Previous month note"), 0644); err != nil {
		t.Fatalf("Failed to write file: %v", err)
	}

	// Create note for first day of current month
	currMonthStr := firstOfMonth.Format("20060102")
	currMonthDir := currMonthStr[:6]
	currMonthFile := filepath.Join(ms.memoryDir, currMonthDir, currMonthStr+".md")
	if err := os.MkdirAll(filepath.Dir(currMonthFile), 0755); err != nil {
		t.Fatalf("Failed to create month directory: %v", err)
	}
	if err := os.WriteFile(currMonthFile, []byte("Current month note"), 0644); err != nil {
		t.Fatalf("Failed to write file: %v", err)
	}

	// Calculate days to request based on date difference
	daysDiff := int(now.Sub(lastDayOfPrevMonth).Hours()/24) + 1
	result := ms.GetRecentDailyNotes(daysDiff)

	// Should contain both notes
	if !strings.Contains(result, "Previous month note") {
		t.Error("Expected result to contain previous month note")
	}
	if !strings.Contains(result, "Current month note") {
		t.Error("Expected result to contain current month note")
	}
}

// ============================================================================
// Tests for GetMemoryContext
// ============================================================================

// TestGetMemoryContext_NoMemory tests when no memory exists
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

// TestGetMemoryContext_OnlyLongTerm tests with only long-term memory
func TestGetMemoryContext_OnlyLongTerm(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ms := NewMemoryStore(tempDir)

	// Write long-term memory
	longTermContent := "Important long-term information"
	if err := ms.WriteLongTerm(longTermContent); err != nil {
		t.Fatalf("Failed to write long-term memory: %v", err)
	}

	result := ms.GetMemoryContext()

	// Should contain memory header and long-term section
	if !strings.Contains(result, "# Memory") {
		t.Error("Expected result to contain '# Memory' header")
	}
	if !strings.Contains(result, "## Long-term Memory") {
		t.Error("Expected result to contain long-term memory section")
	}
	if !strings.Contains(result, longTermContent) {
		t.Error("Expected result to contain long-term content")
	}
	// Should not contain daily notes section
	if strings.Contains(result, "## Recent Daily Notes") {
		t.Error("Expected result to not contain daily notes section when none exist")
	}
}

// TestGetMemoryContext_OnlyDailyNotes tests with only daily notes
func TestGetMemoryContext_OnlyDailyNotes(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ms := NewMemoryStore(tempDir)

	// Create today's note
	if err := ms.AppendToday("Today's activity"); err != nil {
		t.Fatalf("Failed to append today's note: %v", err)
	}

	result := ms.GetMemoryContext()

	// Should contain memory header and daily notes section
	if !strings.Contains(result, "# Memory") {
		t.Error("Expected result to contain '# Memory' header")
	}
	if !strings.Contains(result, "## Recent Daily Notes") {
		t.Error("Expected result to contain daily notes section")
	}
	if !strings.Contains(result, "Today's activity") {
		t.Error("Expected result to contain daily note content")
	}
	// Should not contain long-term section
	if strings.Contains(result, "## Long-term Memory") {
		t.Error("Expected result to not contain long-term section when none exists")
	}
}

// TestGetMemoryContext_BothMemories tests with both long-term and daily notes
func TestGetMemoryContext_BothMemories(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ms := NewMemoryStore(tempDir)

	// Write long-term memory
	longTermContent := "Important facts"
	if err := ms.WriteLongTerm(longTermContent); err != nil {
		t.Fatalf("Failed to write long-term memory: %v", err)
	}

	// Create daily note
	if err := ms.AppendToday("Daily activity"); err != nil {
		t.Fatalf("Failed to append today's note: %v", err)
	}

	result := ms.GetMemoryContext()

	// Should contain both sections
	if !strings.Contains(result, "## Long-term Memory") {
		t.Error("Expected result to contain long-term memory section")
	}
	if !strings.Contains(result, "## Recent Daily Notes") {
		t.Error("Expected result to contain daily notes section")
	}
	if !strings.Contains(result, longTermContent) {
		t.Error("Expected result to contain long-term content")
	}
	if !strings.Contains(result, "Daily activity") {
		t.Error("Expected result to contain daily note content")
	}
	// Should have separator between sections
	if !strings.Contains(result, "---") {
		t.Error("Expected result to contain separator between sections")
	}
}

// TestGetMemoryContext_EmptyLongTerm tests with empty long-term memory file
func TestGetMemoryContext_EmptyLongTerm(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ms := NewMemoryStore(tempDir)

	// Create empty long-term memory file
	if err := ms.WriteLongTerm(""); err != nil {
		t.Fatalf("Failed to write empty long-term memory: %v", err)
	}

	// Create daily note
	if err := ms.AppendToday("Daily activity"); err != nil {
		t.Fatalf("Failed to append today's note: %v", err)
	}

	result := ms.GetMemoryContext()

	// Should not contain long-term section since it's empty
	if strings.Contains(result, "## Long-term Memory") {
		t.Error("Expected result to not contain long-term section when empty")
	}
	// Should contain daily notes
	if !strings.Contains(result, "## Recent Daily Notes") {
		t.Error("Expected result to contain daily notes section")
	}
}

// ============================================================================
// Integration Tests
// ============================================================================

// TestMemoryStore_FullWorkflow tests a complete workflow of memory operations
func TestMemoryStore_FullWorkflow(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ms := NewMemoryStore(tempDir)

	// Step 1: Write long-term memory
	longTermContent := "# User Preferences\n\n- Name: John\n- Language: English"
	if err := ms.WriteLongTerm(longTermContent); err != nil {
		t.Fatalf("Failed to write long-term memory: %v", err)
	}

	// Step 2: Read it back
	readLongTerm := ms.ReadLongTerm()
	if readLongTerm != longTermContent {
		t.Error("Long-term memory read/write mismatch")
	}

	// Step 3: Append to today's notes
	if err := ms.AppendToday("Started working on project"); err != nil {
		t.Fatalf("Failed to append to today's notes: %v", err)
	}

	// Step 4: Read today's notes
	todayNotes := ms.ReadToday()
	if !strings.Contains(todayNotes, "Started working on project") {
		t.Error("Today's notes don't contain expected content")
	}

	// Step 5: Append more to today's notes
	if err := ms.AppendToday("Made good progress"); err != nil {
		t.Fatalf("Failed to append to today's notes: %v", err)
	}

	// Step 6: Get memory context
	context := ms.GetMemoryContext()
	if !strings.Contains(context, "User Preferences") {
		t.Error("Memory context doesn't contain long-term memory")
	}
	if !strings.Contains(context, "Started working on project") {
		t.Error("Memory context doesn't contain first daily note")
	}
	if !strings.Contains(context, "Made good progress") {
		t.Error("Memory context doesn't contain second daily note")
	}

	// Step 7: Get recent daily notes
	recentNotes := ms.GetRecentDailyNotes(1)
	if !strings.Contains(recentNotes, "Started working on project") {
		t.Error("Recent notes don't contain expected content")
	}
}

// TestMemoryStore_ConcurrentAccess tests concurrent access to memory store
func TestMemoryStore_ConcurrentAccess(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lele-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ms := NewMemoryStore(tempDir)

	// Perform concurrent writes
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

	// Wait for all goroutines to complete
	for i := 0; i < 3; i++ {
		<-done
	}

	// Verify final state is consistent (no panics, files exist)
	if _, err := os.Stat(ms.memoryFile); os.IsNotExist(err) {
		t.Error("Long-term memory file should exist after concurrent access")
	}

	todayFile := ms.getTodayFile()
	if _, err := os.Stat(todayFile); os.IsNotExist(err) {
		t.Error("Today's file should exist after concurrent access")
	}
}
