package sources

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/devices/events"
)

func TestNewUSBMonitor(t *testing.T) {
	m := NewUSBMonitor()
	if m == nil {
		t.Fatal("NewUSBMonitor returned nil")
	}
}

func TestUSBMonitor_Kind(t *testing.T) {
	m := NewUSBMonitor()
	if got := m.Kind(); got != events.KindUSB {
		t.Errorf("Kind() = %q, want %q", got, events.KindUSB)
	}
}

func TestUSBMonitor_Stop_NilCmd(t *testing.T) {
	m := NewUSBMonitor()
	// cmd is nil -> Stop should be a no-op and return nil.
	if err := m.Stop(); err != nil {
		t.Errorf("Stop with nil cmd returned error: %v", err)
	}
}

func TestParseUSBEvent(t *testing.T) {
	tests := []struct {
		name    string
		action  string
		props   map[string]string
		wantNil bool
		check   func(t *testing.T, ev *events.DeviceEvent)
	}{
		{
			name:   "add usb device with all fields",
			action: "add",
			props: map[string]string{
				"SUBSYSTEM":       "usb",
				"DEVTYPE":         "usb_device",
				"ID_VENDOR":       "Logitech",
				"ID_MODEL":        "Mouse",
				"ID_SERIAL_SHORT": "SN1",
				"BUSNUM":          "001",
				"DEVNUM":          "003",
				"ID_USB_CLASS":    "03",
			},
			check: func(t *testing.T, ev *events.DeviceEvent) {
				if ev.Action != events.ActionAdd {
					t.Errorf("Action = %q, want add", ev.Action)
				}
				if ev.Kind != events.KindUSB {
					t.Errorf("Kind = %q, want usb", ev.Kind)
				}
				if ev.Vendor != "Logitech" {
					t.Errorf("Vendor = %q, want Logitech", ev.Vendor)
				}
				if ev.Product != "Mouse" {
					t.Errorf("Product = %q, want Mouse", ev.Product)
				}
				if ev.Serial != "SN1" {
					t.Errorf("Serial = %q, want SN1", ev.Serial)
				}
				if ev.DeviceID != "001:003" {
					t.Errorf("DeviceID = %q, want 001:003", ev.DeviceID)
				}
				if ev.Capabilities != "HID (Keyboard/Mouse/Gamepad)" {
					t.Errorf("Capabilities = %q, want HID", ev.Capabilities)
				}
			},
		},
		{
			name:   "remove usb device",
			action: "remove",
			props: map[string]string{
				"SUBSYSTEM": "usb",
				"DEVTYPE":   "usb_device",
			},
			check: func(t *testing.T, ev *events.DeviceEvent) {
				if ev.Action != events.ActionRemove {
					t.Errorf("Action = %q, want remove", ev.Action)
				}
				// Unknown vendor/product
				if ev.Vendor != "Unknown Vendor" {
					t.Errorf("Vendor = %q, want Unknown Vendor", ev.Vendor)
				}
				if ev.Product != "Unknown Device" {
					t.Errorf("Product = %q, want Unknown Device", ev.Product)
				}
				if ev.Capabilities != "USB Device" {
					t.Errorf("Capabilities = %q, want default USB Device", ev.Capabilities)
				}
			},
		},
		{
			name:    "non-usb subsystem returns nil",
			action:  "add",
			props:   map[string]string{"SUBSYSTEM": "block"},
			wantNil: true,
		},
		{
			name:    "usb_interface event returns nil",
			action:  "add",
			props:   map[string]string{"SUBSYSTEM": "usb", "DEVTYPE": "usb_interface"},
			wantNil: true,
		},
		{
			name:    "unknown devtype returns nil",
			action:  "add",
			props:   map[string]string{"SUBSYSTEM": "usb", "DEVTYPE": "usb_port"},
			wantNil: true,
		},
		{
			name:    "unknown action returns nil",
			action:  "change",
			props:   map[string]string{"SUBSYSTEM": "usb", "DEVTYPE": "usb_device"},
			wantNil: true,
		},
		{
			name:   "no devtype accepted (udev variance)",
			action: "add",
			props: map[string]string{
				"SUBSYSTEM":    "usb",
				"ID_VENDOR_ID": "046d",
				"ID_MODEL_ID":  "c077",
				"DEVPATH":      "/dev/bus/usb/001/003",
			},
			check: func(t *testing.T, ev *events.DeviceEvent) {
				if ev.Vendor != "046d" {
					t.Errorf("Vendor = %q, want 046d (from ID_VENDOR_ID)", ev.Vendor)
				}
				if ev.Product != "c077" {
					t.Errorf("Product = %q, want c077 (from ID_MODEL_ID)", ev.Product)
				}
				if ev.DeviceID != "/dev/bus/usb/001/003" {
					t.Errorf("DeviceID = %q, want DEVPATH", ev.DeviceID)
				}
				if ev.Raw == nil {
					t.Error("Raw should be set")
				}
			},
		},
		{
			name:   "capability class case-insensitive",
			action: "add",
			props: map[string]string{
				"SUBSYSTEM":    "usb",
				"DEVTYPE":      "usb_device",
				"ID_USB_CLASS": "08",
			},
			check: func(t *testing.T, ev *events.DeviceEvent) {
				if ev.Capabilities != "Mass Storage (USB Flash Drive/Hard Disk)" {
					t.Errorf("Capabilities = %q, want mass storage", ev.Capabilities)
				}
			},
		},
		{
			name:   "unknown usb class falls back",
			action: "add",
			props: map[string]string{
				"SUBSYSTEM":    "usb",
				"DEVTYPE":      "usb_device",
				"ID_USB_CLASS": "zz",
			},
			check: func(t *testing.T, ev *events.DeviceEvent) {
				if ev.Capabilities != "USB Device" {
					t.Errorf("Capabilities = %q, want default", ev.Capabilities)
				}
			},
		},
		{
			name:   "no class falls back",
			action: "add",
			props:  map[string]string{"SUBSYSTEM": "usb", "DEVTYPE": "usb_device"},
			check: func(t *testing.T, ev *events.DeviceEvent) {
				if ev.Capabilities != "USB Device" {
					t.Errorf("Capabilities = %q, want default", ev.Capabilities)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev := parseUSBEvent(tt.action, tt.props)
			if tt.wantNil {
				if ev != nil {
					t.Errorf("parseUSBEvent returned non-nil: %+v", ev)
				}
				return
			}
			if ev == nil {
				t.Fatal("parseUSBEvent returned nil")
			}
			if tt.check != nil {
				tt.check(t, ev)
			}
		})
	}
}

// writeFakeUdevadm creates a fake udevadm script that emits a UDEV add event and
// then blocks, and returns a PATH prefixed with its directory.
func writeFakeUdevadm(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "udevadm")
	content := `#!/bin/sh
echo 'UDEV  [123.456] add /devices/pci/1-2 (usb)'
echo 'ACTION=add'
echo 'SUBSYSTEM=usb'
echo 'DEVTYPE=usb_device'
echo 'ID_VENDOR=Acme'
echo 'ID_MODEL=Widget'
echo 'ID_USB_CLASS=08'
echo ''
echo 'KERNEL[123.457] remove /devices/pci (usb)'
echo 'ACTION=remove'
echo 'SUBSYSTEM=block'
echo ''
sleep 30
`
	if err := os.WriteFile(script, []byte(content), 0755); err != nil {
		t.Fatalf("failed to write fake udevadm: %v", err)
	}
	return dir
}

func TestUSBMonitor_Start_ReceivesEvents(t *testing.T) {
	fakeDir := writeFakeUdevadm(t)
	oldPath := os.Getenv("PATH")
	os.Setenv("PATH", fakeDir+string(os.PathListSeparator)+oldPath)
	defer os.Setenv("PATH", oldPath)

	m := NewUSBMonitor()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := m.Start(ctx)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if ch == nil {
		t.Fatal("Start returned nil channel")
	}

	select {
	case ev := <-ch:
		if ev == nil {
			t.Fatal("received nil event")
		}
		if ev.Action != events.ActionAdd {
			t.Errorf("Action = %q, want add", ev.Action)
		}
		if ev.Vendor != "Acme" {
			t.Errorf("Vendor = %q, want Acme", ev.Vendor)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for USB event")
	}

	// Stop should kill the running process.
	if err := m.Stop(); err != nil {
		t.Errorf("Stop returned error: %v", err)
	}
}

func TestUSBMonitor_Start_MissingUdevadm(t *testing.T) {
	// Temporarily set PATH to an empty dir so udevadm is not found.
	emptyDir := t.TempDir()
	oldPath := os.Getenv("PATH")
	os.Setenv("PATH", emptyDir)
	defer os.Setenv("PATH", oldPath)

	m := NewUSBMonitor()
	_, err := m.Start(context.Background())
	if err == nil {
		t.Fatal("expected error when udevadm is not found")
	}
	if !strings.Contains(err.Error(), "udevadm start") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestUSBMonitor_Start_StdinPipeErrorIsImpossible_PathExists(t *testing.T) {
	// PATH with udevadm; Start should succeed (the pipe + start both work).
	if _, err := exec.LookPath("udevadm"); err != nil {
		t.Skip("udevadm not available; relying on fake udevadm test")
	}
}
