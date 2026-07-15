package tools

import (
	"context"
	"testing"
	"time"
)

func TestSleepTool_Name(t *testing.T) {
	tool := NewSleepTool()
	if tool.Name() != "sleep" {
		t.Errorf("Expected name 'sleep', got '%s'", tool.Name())
	}
}

func TestSleepTool_Description(t *testing.T) {
	tool := NewSleepTool()
	desc := tool.Description()
	if desc == "" {
		t.Error("Description should not be empty")
	}
}

func TestSleepTool_Parameters(t *testing.T) {
	tool := NewSleepTool()
	params := tool.Parameters()
	if params == nil {
		t.Fatal("Parameters should not be nil")
	}
	if params["type"] != "object" {
		t.Errorf("Expected type 'object', got %v", params["type"])
	}
	props, ok := params["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("Properties should be a map")
	}
	seconds, ok := props["seconds"].(map[string]interface{})
	if !ok {
		t.Fatal("seconds parameter should exist")
	}
	if seconds["type"] != "number" {
		t.Errorf("Expected seconds type 'number', got %v", seconds["type"])
	}
}

func TestSleepTool_Execute_Success(t *testing.T) {
	tool := NewSleepTool()
	ctx := context.Background()
	args := map[string]interface{}{"seconds": 0.2}

	start := time.Now()
	result := tool.Execute(ctx, args)
	elapsed := time.Since(start)

	if result == nil {
		t.Fatal("Result should not be nil")
	}
	if result.IsError {
		t.Errorf("Expected success, got error: %s", result.ForLLM)
	}
	if !result.Silent {
		t.Error("Sleep should be silent")
	}
	if elapsed < 150*time.Millisecond {
		t.Errorf("Expected to sleep at least 150ms, got %v", elapsed)
	}
}

func TestSleepTool_Execute_MissingSeconds(t *testing.T) {
	tool := NewSleepTool()
	ctx := context.Background()
	args := map[string]interface{}{}

	result := tool.Execute(ctx, args)

	if !result.IsError {
		t.Error("Expected error for missing seconds")
	}
	if result.ForLLM == "" {
		t.Error("Error message should not be empty")
	}
}

func TestSleepTool_Execute_TooSmall(t *testing.T) {
	tool := NewSleepTool()
	ctx := context.Background()
	args := map[string]interface{}{"seconds": 0.01}

	result := tool.Execute(ctx, args)

	if !result.IsError {
		t.Error("Expected error for seconds < 0.1")
	}
}

func TestSleepTool_Execute_TooLarge(t *testing.T) {
	tool := NewSleepTool()
	ctx := context.Background()
	args := map[string]interface{}{"seconds": 301.0}

	result := tool.Execute(ctx, args)

	if !result.IsError {
		t.Error("Expected error for seconds > 300")
	}
}

func TestSleepTool_Execute_ContextCancellation(t *testing.T) {
	tool := NewSleepTool()
	ctx, cancel := context.WithCancel(context.Background())

	// Cancel after 50ms
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	args := map[string]interface{}{"seconds": 10.0}
	start := time.Now()
	result := tool.Execute(ctx, args)
	elapsed := time.Since(start)

	if !result.IsError {
		t.Error("Expected error for interrupted sleep")
	}
	// Should return well before 10 seconds.
	if elapsed > 2*time.Second {
		t.Errorf("Expected sleep to be interrupted quickly, took %v", elapsed)
	}
}
