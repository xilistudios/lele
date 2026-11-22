//go:build linux

package sources

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"

	"github.com/xilistudios/lele/pkg/devices/events"
	"github.com/xilistudios/lele/pkg/logger"
)

var usbClassToCapability = map[string]string{
	"00": "Interface Definition (by interface)",
	"01": "Audio",
	"02": "CDC Communication (Network Card/Modem)",
	"03": "HID (Keyboard/Mouse/Gamepad)",
	"05": "Physical Interface",
	"06": "Image (Scanner/Camera)",
	"07": "Printer",
	"08": "Mass Storage (USB Flash Drive/Hard Disk)",
	"09": "USB Hub",
	"0a": "CDC Data",
	"0b": "Smart Card",
	"0e": "Video (Camera)",
	"dc": "Diagnostic Device",
	"e0": "Wireless Controller (Bluetooth)",
	"ef": "Miscellaneous",
	"fe": "Application Specific",
	"ff": "Vendor Specific",
}

type USBMonitor struct {
	cmd    *exec.Cmd
	cancel context.CancelFunc
	mu     sync.Mutex
}

func NewUSBMonitor() *USBMonitor {
	return &USBMonitor{}
}

func (m *USBMonitor) Kind() events.Kind {
	return events.KindUSB
}

func (m *USBMonitor) Start(ctx context.Context) (<-chan *events.DeviceEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cmd := exec.CommandContext(ctx, "udevadm", "monitor", "--property", "--subsystem-match=usb")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("udevadm stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("udevadm start: %w (is udevadm installed?)", err)
	}

	m.cmd = cmd
	eventCh := make(chan *events.DeviceEvent, 16)

	go func() {
		defer close(eventCh)
		scanner := bufio.NewScanner(stdout)
		var props map[string]string
		var action string
		isUdev := false

		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				if isUdev && props != nil && (action == "add" || action == "remove") {
					if ev := parseUSBEvent(action, props); ev != nil {
						select {
						case eventCh <- ev:
						case <-ctx.Done():
							return
						}
					}
				}
				props = nil
				action = ""
				isUdev = false
				continue
			}

			idx := strings.Index(line, "=")
			if idx <= 0 {
				isUdev = strings.HasPrefix(strings.TrimSpace(line), "UDEV")
				continue
			}

			key := line[:idx]
			val := line[idx+1:]
			if props == nil {
				props = make(map[string]string)
			}
			props[key] = val

			if key == "ACTION" {
				action = val
			}
		}

		if err := scanner.Err(); err != nil {
			logger.ErrorCF("devices", "udevadm scan error", map[string]interface{}{"error": err.Error()})
		}
		if err := cmd.Wait(); err != nil {
			logger.ErrorCF("devices", "udevadm process wait error", map[string]interface{}{"error": err.Error()})
		}
	}()

	return eventCh, nil
}

func (m *USBMonitor) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd != nil && m.cmd.Process != nil {
		_ = m.cmd.Process.Kill()
		m.cmd = nil
	}
	return nil
}

func parseUSBEvent(action string, props map[string]string) *events.DeviceEvent {
	subsystem := props["SUBSYSTEM"]
	if subsystem != "usb" {
		return nil
	}
	devType := props["DEVTYPE"]
	if devType == "usb_interface" {
		return nil
	}
	if devType != "" && devType != "usb_device" {
		return nil
	}

	ev := &events.DeviceEvent{
		Raw: props,
	}
	switch action {
	case "add":
		ev.Action = events.ActionAdd
	case "remove":
		ev.Action = events.ActionRemove
	default:
		return nil
	}
	ev.Kind = events.KindUSB

	ev.Vendor = props["ID_VENDOR"]
	if ev.Vendor == "" {
		ev.Vendor = props["ID_VENDOR_ID"]
	}
	if ev.Vendor == "" {
		ev.Vendor = "Unknown Vendor"
	}

	ev.Product = props["ID_MODEL"]
	if ev.Product == "" {
		ev.Product = props["ID_MODEL_ID"]
	}
	if ev.Product == "" {
		ev.Product = "Unknown Device"
	}

	ev.Serial = props["ID_SERIAL_SHORT"]
	ev.DeviceID = props["DEVPATH"]
	if bus := props["BUSNUM"]; bus != "" {
		if dev := props["DEVNUM"]; dev != "" {
			ev.DeviceID = bus + ":" + dev
		}
	}

	if class := props["ID_USB_CLASS"]; class != "" {
		ev.Capabilities = usbClassToCapability[strings.ToLower(class)]
	}
	if ev.Capabilities == "" {
		ev.Capabilities = "USB Device"
	}

	return ev
}
