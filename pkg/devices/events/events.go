package events

import (
	"context"
	"strings"
	"unicode"
)

type EventSource interface {
	Kind() Kind
	Start(ctx context.Context) (<-chan *DeviceEvent, error)
	Stop() error
}

type Action string

const (
	ActionAdd    Action = "add"
	ActionRemove Action = "remove"
	ActionChange Action = "change"
)

type Kind string

const (
	KindUSB       Kind = "usb"
	KindBluetooth Kind = "bluetooth"
	KindPCI       Kind = "pci"
	KindGeneric   Kind = "generic"
)

type DeviceEvent struct {
	Action       Action
	Kind         Kind
	DeviceID     string
	Vendor       string
	Product      string
	Serial       string
	Capabilities string
	Raw          map[string]string
}

// maxDescriptorLength caps a descriptor after sanitization. Descriptors come
// from udev properties, i.e. from whatever the device (or a local udev rule)
// chose to report, so nothing bounds them upstream.
const maxDescriptorLength = 128

// sanitizeDescriptor makes a device descriptor safe to interpolate into an
// outbound message: control characters are dropped, every whitespace run
// collapses to one space (so a descriptor cannot forge message lines), markdown
// backticks are escaped (so it cannot open a code span someone else closes), and
// an over-long value is truncated with a visible mark.
func sanitizeDescriptor(value string) string {
	cleaned := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			if unicode.IsSpace(r) {
				return ' '
			}
			return -1
		}
		return r
	}, value)
	cleaned = strings.Join(strings.Fields(cleaned), " ")

	if runes := []rune(cleaned); len(runes) > maxDescriptorLength {
		cleaned = string(runes[:maxDescriptorLength]) + "…"
	}

	return strings.ReplaceAll(cleaned, "`", "\\`")
}

func (e *DeviceEvent) FormatMessage() string {
	actionEmoji := "🔌"
	actionText := "Connected"
	if e.Action == ActionRemove {
		actionEmoji = "🔌"
		actionText = "Disconnected"
	}

	msg := actionEmoji + " Device " + actionText + "\n\n"
	msg += "Type: " + string(e.Kind) + "\n"
	msg += "Device: " + sanitizeDescriptor(e.Vendor) + " " + sanitizeDescriptor(e.Product) + "\n"
	if e.Capabilities != "" {
		msg += "Capabilities: " + sanitizeDescriptor(e.Capabilities) + "\n"
	}
	if e.Serial != "" {
		msg += "Serial: " + sanitizeDescriptor(e.Serial) + "\n"
	}
	return msg
}
