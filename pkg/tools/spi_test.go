package tools

import (
	"context"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestSPITool_Name(t *testing.T) {
	if NewSPITool().Name() != "spi" {
		t.Fatal("expected name 'spi'")
	}
}

func TestSPITool_Description(t *testing.T) {
	if NewSPITool().Description() == "" {
		t.Fatal("expected non-empty description")
	}
}

func TestSPITool_Parameters(t *testing.T) {
	params := NewSPITool().Parameters()
	props, ok := params["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("expected properties")
	}
	for _, key := range []string{"action", "device", "speed", "mode", "bits", "data", "length", "confirm"} {
		if _, ok := props[key]; !ok {
			t.Errorf("missing property %q", key)
		}
	}
}

func TestSPITool_Execute_missingAction(t *testing.T) {
	result := NewSPITool().Execute(context.Background(), map[string]interface{}{})
	if result == nil || !result.IsError {
		t.Fatal("expected error for missing action")
	}
	if !strings.Contains(result.ForLLM, "action is required") {
		t.Fatalf("ForLLM = %q", result.ForLLM)
	}
}

func TestSPITool_Execute_unknownAction(t *testing.T) {
	result := NewSPITool().Execute(context.Background(), map[string]interface{}{
		"action": "launch",
	})
	if result == nil || !result.IsError {
		t.Fatal("expected error for unknown action")
	}
	if !strings.Contains(result.ForLLM, "unknown action") {
		t.Fatalf("ForLLM = %q", result.ForLLM)
	}
}

func TestSPITool_Execute_list(t *testing.T) {
	result := NewSPITool().Execute(context.Background(), map[string]interface{}{
		"action": "list",
	})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	// Either a silent no-devices result or a found-list is valid, never an error.
	if result.IsError {
		t.Fatalf("list returned error: %s", result.ForLLM)
	}
}

func TestSPITool_Execute_transferNoConfirm(t *testing.T) {
	result := NewSPITool().Execute(context.Background(), map[string]interface{}{
		"action": "transfer",
	})
	if result == nil || !result.IsError {
		t.Fatal("expected error for transfer without confirm")
	}
	if !strings.Contains(result.ForLLM, "confirm: true") {
		t.Fatalf("ForLLM = %q", result.ForLLM)
	}
}

func TestSPITool_Execute_transferNoDevice(t *testing.T) {
	result := NewSPITool().Execute(context.Background(), map[string]interface{}{
		"action":  "transfer",
		"confirm": true,
	})
	if result == nil || !result.IsError {
		t.Fatal("expected error for transfer with no device")
	}
	if !strings.Contains(result.ForLLM, "device is required") {
		t.Fatalf("ForLLM = %q", result.ForLLM)
	}
}

func TestSPITool_Execute_transferNoData(t *testing.T) {
	result := NewSPITool().Execute(context.Background(), map[string]interface{}{
		"action":  "transfer",
		"confirm": true,
		"device":  "2.0",
	})
	if result == nil || !result.IsError {
		t.Fatal("expected error for transfer with no data")
	}
	if !strings.Contains(result.ForLLM, "data is required") {
		t.Fatalf("ForLLM = %q", result.ForLLM)
	}
}

func TestSPITool_Execute_transferDataTooLong(t *testing.T) {
	var data []interface{}
	for i := 0; i < 4097; i++ {
		data = append(data, float64(i%256))
	}
	result := NewSPITool().Execute(context.Background(), map[string]interface{}{
		"action":  "transfer",
		"confirm": true,
		"device":  "2.0",
		"data":    data,
	})
	if result == nil || !result.IsError {
		t.Fatal("expected error for data too long")
	}
	if !strings.Contains(result.ForLLM, "4096") {
		t.Fatalf("ForLLM = %q", result.ForLLM)
	}
}

func TestSPITool_Execute_transferInvalidDataElement(t *testing.T) {
	result := NewSPITool().Execute(context.Background(), map[string]interface{}{
		"action":  "transfer",
		"confirm": true,
		"device":  "2.0",
		"data":    []interface{}{"x"},
	})
	if result == nil || !result.IsError {
		t.Fatal("expected error for invalid data element")
	}
	if !strings.Contains(result.ForLLM, "not a valid byte value") {
		t.Fatalf("ForLLM = %q", result.ForLLM)
	}
}

func TestSPITool_Execute_transferOutOfRangeData(t *testing.T) {
	result := NewSPITool().Execute(context.Background(), map[string]interface{}{
		"action":  "transfer",
		"confirm": true,
		"device":  "2.0",
		"data":    []interface{}{float64(256)},
	})
	if result == nil || !result.IsError {
		t.Fatal("expected error for out-of-range data")
	}
	if !strings.Contains(result.ForLLM, "out of byte range") {
		t.Fatalf("ForLLM = %q", result.ForLLM)
	}
}

func TestSPITool_Execute_readNoDevice(t *testing.T) {
	result := NewSPITool().Execute(context.Background(), map[string]interface{}{
		"action": "read",
	})
	if result == nil || !result.IsError {
		t.Fatal("expected error for read with no device")
	}
}

func TestSPITool_Execute_readInvalidLength(t *testing.T) {
	result := NewSPITool().Execute(context.Background(), map[string]interface{}{
		"action": "read",
		"device": "2.0",
		"length": float64(5000),
	})
	if result == nil || !result.IsError {
		t.Fatal("expected error for read length 5000")
	}
	if !strings.Contains(result.ForLLM, "length is required") {
		t.Fatalf("ForLLM = %q", result.ForLLM)
	}
}

// Unit tests for parseSPIArgs.
func TestParseSPIArgs_defaults(t *testing.T) {
	dev, speed, mode, bits, errMsg := parseSPIArgs(map[string]interface{}{
		"device": "2.0",
	})
	if errMsg != "" {
		t.Fatalf("errMsg = %q", errMsg)
	}
	if dev != "2.0" {
		t.Fatalf("dev = %q", dev)
	}
	if speed != 1000000 {
		t.Fatalf("speed = %d, want default 1MHz", speed)
	}
	if mode != 0 {
		t.Fatalf("mode = %d, want 0", mode)
	}
	if bits != 8 {
		t.Fatalf("bits = %d, want 8", bits)
	}
}

func TestParseSPIArgs_missingDevice(t *testing.T) {
	_, _, _, _, errMsg := parseSPIArgs(map[string]interface{}{})
	if !strings.Contains(errMsg, "device is required") {
		t.Fatalf("errMsg = %q", errMsg)
	}
}

func TestParseSPIArgs_invalidDevice(t *testing.T) {
	_, _, _, _, errMsg := parseSPIArgs(map[string]interface{}{"device": "two"})
	if !strings.Contains(errMsg, "invalid device identifier") {
		t.Fatalf("errMsg = %q", errMsg)
	}
}

func TestParseSPIArgs_invalidSpeed(t *testing.T) {
	_, _, _, _, errMsg := parseSPIArgs(map[string]interface{}{
		"device": "2.0",
		"speed":  float64(0),
	})
	if !strings.Contains(errMsg, "speed must be") {
		t.Fatalf("errMsg = %q", errMsg)
	}

	_, _, _, _, errMsg = parseSPIArgs(map[string]interface{}{
		"device": "2.0",
		"speed":  float64(125000001),
	})
	if !strings.Contains(errMsg, "speed must be") {
		t.Fatalf("errMsg = %q", errMsg)
	}
}

func TestParseSPIArgs_validSpeed(t *testing.T) {
	_, speed, _, _, errMsg := parseSPIArgs(map[string]interface{}{
		"device": "2.0",
		"speed":  float64(500000),
	})
	if errMsg != "" {
		t.Fatalf("errMsg = %q", errMsg)
	}
	if speed != 500000 {
		t.Fatalf("speed = %d", speed)
	}
}

func TestParseSPIArgs_invalidMode(t *testing.T) {
	_, _, _, _, errMsg := parseSPIArgs(map[string]interface{}{
		"device": "2.0",
		"mode":   float64(4),
	})
	if !strings.Contains(errMsg, "mode must be 0-3") {
		t.Fatalf("errMsg = %q", errMsg)
	}
}

func TestParseSPIArgs_validMode(t *testing.T) {
	_, _, mode, _, errMsg := parseSPIArgs(map[string]interface{}{
		"device": "2.0",
		"mode":   float64(3),
	})
	if errMsg != "" {
		t.Fatalf("errMsg = %q", errMsg)
	}
	if mode != 3 {
		t.Fatalf("mode = %d", mode)
	}
}

func TestParseSPIArgs_invalidBits(t *testing.T) {
	_, _, _, _, errMsg := parseSPIArgs(map[string]interface{}{
		"device": "2.0",
		"bits":   float64(0),
	})
	if !strings.Contains(errMsg, "bits must be") {
		t.Fatalf("errMsg = %q", errMsg)
	}

	_, _, _, _, errMsg = parseSPIArgs(map[string]interface{}{
		"device": "2.0",
		"bits":   float64(33),
	})
	if !strings.Contains(errMsg, "bits must be") {
		t.Fatalf("errMsg = %q", errMsg)
	}
}

func TestParseSPIArgs_validBits(t *testing.T) {
	_, _, _, bits, errMsg := parseSPIArgs(map[string]interface{}{
		"device": "2.0",
		"bits":   float64(16),
	})
	if errMsg != "" {
		t.Fatalf("errMsg = %q", errMsg)
	}
	if bits != 16 {
		t.Fatalf("bits = %d", bits)
	}
}// listSPIDevices returns an existing /dev/spidev* path for real-device tests.
func listSPIDevices() ([]string, error) {
	return filepath.Glob("/dev/spidev*")
}

func syscallClose(fd int) { syscall.Close(fd) }

// --- spi_linux.go coverage on Linux ---

// TestSPITool_Execute_transferOpenFailure covers the configureSPI open-error
// path (valid args, but the device file does not exist).
func TestSPITool_Execute_transferOpenFailure(t *testing.T) {
	result := NewSPITool().Execute(context.Background(), map[string]interface{}{
		"action":  "transfer",
		"confirm": true,
		"device":  "99.99",
		"data":    []interface{}{float64(0xAA), float64(0xBB)},
	})
	if result == nil {
		t.Fatal("nil result")
	}
	if !result.IsError {
		t.Fatalf("expected error (open failure), got %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "failed to open") {
		t.Fatalf("ForLLM = %q", result.ForLLM)
	}
}

// TestSPITool_Execute_readOpenFailure covers readDevice's configureSPI failure.
func TestSPITool_Execute_readOpenFailure(t *testing.T) {
	result := NewSPITool().Execute(context.Background(), map[string]interface{}{
		"action": "read",
		"device": "99.99",
		"length": float64(4),
	})
	if result == nil || !result.IsError {
		t.Fatalf("expected error, got %+v", result)
	}
	if !strings.Contains(result.ForLLM, "failed to open") {
		t.Fatalf("ForLLM = %q", result.ForLLM)
	}
}

// TestConfigureSPI_nonexistentPath covers the configureSPI open-error branch.
func TestConfigureSPI_nonexistentPath(t *testing.T) {
	fd, res := configureSPI("/dev/spidev99.99", 0, 8, 1000000)
	if fd != -1 {
		syscallClose(fd)
		t.Fatalf("expected fd=-1, got %d", fd)
	}
	if res == nil || !res.IsError {
		t.Fatal("expected error result from configureSPI on nonexistent device")
	}
	if !strings.Contains(res.ForLLM, "failed to open") {
		t.Fatalf("ForLLM = %q", res.ForLLM)
	}
}

// TestConfigureSPI_validPathIsDeviceDependent skips if no spidev present, else
// attempts a real configure to verify it returns a valid fd or a clean error.
func TestConfigureSPI_realDeviceOrSkip(t *testing.T) {
	matches, err := listSPIDevices()
	if err != nil || len(matches) == 0 {
		t.Skip("no SPI devices available on this host")
	}
	fd, res := configureSPI(matches[0], 0, 8, 1000000)
	if res != nil {
		t.Skipf("device %s not usable, err: %s", matches[0], res.ForLLM)
	}
	if fd < 0 {
		t.Fatalf("expected valid fd, got %d", fd)
	}
	syscallClose(fd)
}