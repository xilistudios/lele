package tools

import (
	"context"
	"strings"
	"testing"
)

func TestI2CTool_Name(t *testing.T) {
	if NewI2CTool().Name() != "i2c" {
		t.Fatal("expected name 'i2c'")
	}
}

func TestI2CTool_Description(t *testing.T) {
	if NewI2CTool().Description() == "" {
		t.Fatal("expected non-empty description")
	}
}

func TestI2CTool_Parameters(t *testing.T) {
	params := NewI2CTool().Parameters()
	props, ok := params["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("expected properties")
	}
	for _, key := range []string{"action", "bus", "address", "register", "data", "length", "confirm"} {
		if _, ok := props[key]; !ok {
			t.Errorf("missing property %q", key)
		}
	}
	required := params["required"].([]string)
	if len(required) != 1 || required[0] != "action" {
		t.Fatalf("required = %v", required)
	}
}

func TestI2CTool_Execute_missingAction(t *testing.T) {
	result := NewI2CTool().Execute(context.Background(), map[string]interface{}{})
	if result == nil || !result.IsError {
		t.Fatal("expected error for missing action")
	}
	if !strings.Contains(result.ForLLM, "action is required") {
		t.Fatalf("ForLLM = %q", result.ForLLM)
	}
}

func TestI2CTool_Execute_unknownAction(t *testing.T) {
	result := NewI2CTool().Execute(context.Background(), map[string]interface{}{
		"action": "explode",
	})
	if result == nil || !result.IsError {
		t.Fatal("expected error for unknown action")
	}
	if !strings.Contains(result.ForLLM, "unknown action") {
		t.Fatalf("ForLLM = %q", result.ForLLM)
	}
}

// TestI2CTool_Execute_detect covers the detect() path. On a machine without
// /dev/i2c-* devices it returns the "No I2C buses found" silent result. Either
// way the function should not panic and return a valid result.
func TestI2CTool_Execute_detect(t *testing.T) {
	result := NewI2CTool().Execute(context.Background(), map[string]interface{}{
		"action": "detect",
	})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		t.Fatalf("detect returned error: %s", result.ForLLM)
	}
}

// TestI2CTool_Execute_scanMissingBus verifies scan requires a bus.
func TestI2CTool_Execute_scanMissingBus(t *testing.T) {
	result := NewI2CTool().Execute(context.Background(), map[string]interface{}{
		"action": "scan",
	})
	if result == nil || !result.IsError {
		t.Fatal("expected error when scan has no bus")
	}
	if !strings.Contains(result.ForLLM, "bus is required") {
		t.Fatalf("ForLLM = %q", result.ForLLM)
	}
}

// TestI2CTool_Execute_scanInvalidBus verifies bus validation.
func TestI2CTool_Execute_scanInvalidBus(t *testing.T) {
	result := NewI2CTool().Execute(context.Background(), map[string]interface{}{
		"action": "scan",
		"bus":    "abc; rm -rf /",
	})
	if result == nil || !result.IsError {
		t.Fatal("expected error for invalid bus")
	}
	if !strings.Contains(result.ForLLM, "invalid bus identifier") {
		t.Fatalf("ForLLM = %q", result.ForLLM)
	}
}

// TestI2CTool_Execute_scanValidBusNoDevice covers a valid numeric bus — the
// /dev/i2c-N open will fail on CI without the device, returning an error result
// that we accept (no panic, no crash).
func TestI2CTool_Execute_scanValidBus(t *testing.T) {
	result := NewI2CTool().Execute(context.Background(), map[string]interface{}{
		"action": "scan",
		"bus":    "12345",
	})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	// Either an error (open failed) or a silent scan result is fine.
}

func TestI2CTool_Execute_readMissingBus(t *testing.T) {
	result := NewI2CTool().Execute(context.Background(), map[string]interface{}{
		"action":  "read",
		"address": float64(0x38),
	})
	if result == nil || !result.IsError {
		t.Fatal("expected error for read with no bus")
	}
}

func TestI2CTool_Execute_writeNoConfirm(t *testing.T) {
	result := NewI2CTool().Execute(context.Background(), map[string]interface{}{
		"action":  "write",
		"bus":     "1",
		"address": float64(0x38),
		"data":    []interface{}{float64(0xFF)},
	})
	if result == nil || !result.IsError {
		t.Fatal("expected error for write without confirm")
	}
	if !strings.Contains(result.ForLLM, "confirm: true") {
		t.Fatalf("ForLLM = %q", result.ForLLM)
	}
}

func TestI2CTool_Execute_writeMissingData(t *testing.T) {
	result := NewI2CTool().Execute(context.Background(), map[string]interface{}{
		"action":  "write",
		"bus":     "1",
		"address": float64(0x38),
		"confirm": true,
	})
	if result == nil || !result.IsError {
		t.Fatal("expected error for write with no data")
	}
	if !strings.Contains(result.ForLLM, "data is required") {
		t.Fatalf("ForLLM = %q", result.ForLLM)
	}
}

func TestI2CTool_Execute_writeDataTooLong(t *testing.T) {
	var data []interface{}
	for i := 0; i < 257; i++ {
		data = append(data, float64(i))
	}
	result := NewI2CTool().Execute(context.Background(), map[string]interface{}{
		"action":  "write",
		"bus":     "1",
		"address": float64(0x38),
		"confirm": true,
		"data":    data,
	})
	if result == nil || !result.IsError {
		t.Fatal("expected error for data too long")
	}
	if !strings.Contains(result.ForLLM, "256 bytes") {
		t.Fatalf("ForLLM = %q", result.ForLLM)
	}
}

func TestI2CTool_Execute_writeInvalidDataValue(t *testing.T) {
	result := NewI2CTool().Execute(context.Background(), map[string]interface{}{
		"action":  "write",
		"bus":     "1",
		"address": float64(0x38),
		"confirm": true,
		"data":    []interface{}{"not-a-number"},
	})
	if result == nil || !result.IsError {
		t.Fatal("expected error for invalid data element")
	}
	if !strings.Contains(result.ForLLM, "not a valid byte value") {
		t.Fatalf("ForLLM = %q", result.ForLLM)
	}
}

func TestI2CTool_Execute_writeOutOfRangeData(t *testing.T) {
	result := NewI2CTool().Execute(context.Background(), map[string]interface{}{
		"action":  "write",
		"bus":     "1",
		"address": float64(0x38),
		"confirm": true,
		"data":    []interface{}{float64(300)},
	})
	if result == nil || !result.IsError {
		t.Fatal("expected error for out-of-range data")
	}
	if !strings.Contains(result.ForLLM, "out of byte range") {
		t.Fatalf("ForLLM = %q", result.ForLLM)
	}
}

func TestI2CTool_Execute_writeInvalidRegister(t *testing.T) {
	result := NewI2CTool().Execute(context.Background(), map[string]interface{}{
		"action":   "write",
		"bus":      "1",
		"address":  float64(0x38),
		"confirm":  true,
		"data":     []interface{}{float64(1)},
		"register": float64(999),
	})
	if result == nil || !result.IsError {
		t.Fatal("expected error for invalid register")
	}
	if !strings.Contains(result.ForLLM, "register must be") {
		t.Fatalf("ForLLM = %q", result.ForLLM)
	}
}

// parseI2C specific unit tests.
func TestParseI2CAddress_missing(t *testing.T) {
	_, res := parseI2CAddress(map[string]interface{}{})
	if res == nil || !res.IsError {
		t.Fatal("expected error for missing address")
	}
	if !strings.Contains(res.ForLLM, "address is required") {
		t.Fatalf("ForLLM = %q", res.ForLLM)
	}
}

func TestParseI2CAddress_outOfRange(t *testing.T) {
	_, res := parseI2CAddress(map[string]interface{}{"address": float64(0x02)})
	if res == nil || !res.IsError {
		t.Fatal("expected error for out-of-range address")
	}
	if !strings.Contains(res.ForLLM, "range") {
		t.Fatalf("ForLLM = %q", res.ForLLM)
	}

	_, res = parseI2CAddress(map[string]interface{}{"address": float64(0x78)})
	if res == nil || !res.IsError {
		t.Fatal("expected error for address > 0x77")
	}
}

func TestParseI2CAddress_valid(t *testing.T) {
	addr, res := parseI2CAddress(map[string]interface{}{"address": float64(0x38)})
	if res != nil {
		t.Fatalf("unexpected error: %v", res.ForLLM)
	}
	if addr != 0x38 {
		t.Fatalf("addr = %d, want 0x38", addr)
	}
}

func TestParseI2CBus_missing(t *testing.T) {
	_, res := parseI2CBus(map[string]interface{}{})
	if res == nil || !res.IsError {
		t.Fatal("expected error for missing bus")
	}
}

func TestParseI2CBus_invalid(t *testing.T) {
	_, res := parseI2CBus(map[string]interface{}{"bus": "abc"})
	if res == nil || !res.IsError {
		t.Fatal("expected error for invalid bus")
	}
	if !strings.Contains(res.ForLLM, "invalid bus identifier") {
		t.Fatalf("ForLLM = %q", res.ForLLM)
	}
}

func TestParseI2CBus_valid(t *testing.T) {
	bus, res := parseI2CBus(map[string]interface{}{"bus": "7"})
	if res != nil {
		t.Fatalf("unexpected error: %v", res.ForLLM)
	}
	if bus != "7" {
		t.Fatalf("bus = %q, want 7", bus)
	}
}

func TestIsValidBusID(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"1", true},
		{"0", true},
		{"12", true},
		{"abc", false},
		{"1a", false},
		{"", false},
		{"1;rm", false},
	}
	for _, tt := range tests {
		if got := isValidBusID(tt.in); got != tt.want {
			t.Errorf("isValidBusID(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

// TestI2CTool_Execute_readInvalidLength verifies length bounds.
func TestI2CTool_Execute_readInvalidLength(t *testing.T) {
	result := NewI2CTool().Execute(context.Background(), map[string]interface{}{
		"action":  "read",
		"bus":     "1",
		"address": float64(0x38),
		"length":  float64(300),
	})
	if result == nil || !result.IsError {
		t.Fatal("expected error for read length 300")
	}
	if !strings.Contains(result.ForLLM, "length must be") {
		t.Fatalf("ForLLM = %q", result.ForLLM)
	}
}

// TestI2CTool_Execute_readInvalidAddress verifies read requires valid address.
func TestI2CTool_Execute_readInvalidAddress(t *testing.T) {
	result := NewI2CTool().Execute(context.Background(), map[string]interface{}{
		"action": "read",
		"bus":    "1",
	})
	if result == nil || !result.IsError {
		t.Fatal("expected error for read with no address")
	}
}

// TestI2CTool_Execute_readValidatingRegister verifies register is parsed before
// opening device (clear error path), requires bus+addr to be valid first.
func TestI2CTool_Execute_readRegisterOutOfRange(t *testing.T) {
	result := NewI2CTool().Execute(context.Background(), map[string]interface{}{
		"action":   "read",
		"bus":      "1",
		"address":  float64(0x38),
		"register": float64(-5),
	})
	if result == nil || !result.IsError {
		t.Fatal("expected error for invalid register on read")
	}
}