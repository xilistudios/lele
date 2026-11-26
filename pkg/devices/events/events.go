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

// markdownMetas are the characters the channel renderers turn into markup
// anywhere in a line: a backtick opens a code span, * and _ open bold or
// italics, ~ opens strikethrough, and [ ] open link text (which only needs the
// parenthesis that follows to become a clickable anchor). A USB descriptor is
// fully attacker-controlled, so they are removed instead of escaped: escaping
// leaves visible backslashes and the renderer's pair-matching ignores them.
// Parentheses, #, > and | cannot form markup without those characters or a line
// start, and a sanitized descriptor never carries a newline.
const markdownMetas = "`*_~[]"

// sanitizeDescriptor makes a device descriptor safe to interpolate into an
// outbound message: control characters and Unicode format controls (RLO, ZWSP,
// isolates — they reorder or hide text without being control characters) are
// dropped, every whitespace run collapses to one space so a descriptor cannot
// forge message lines, markdown metas are removed so the rendered message stays
// plain text, and an over-long value is truncated with a visible mark.
func sanitizeDescriptor(value string) string {
	cleaned := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			if unicode.IsSpace(r) {
				return ' '
			}
			return -1
		}
		if unicode.In(r, unicode.Cf) {
			return -1
		}
		return r
	}, value)
	cleaned = strings.Map(func(r rune) rune {
		if strings.ContainsRune(markdownMetas, r) {
			return -1
		}
		return r
	}, cleaned)
	cleaned = strings.Join(strings.Fields(cleaned), " ")

	if runes := []rune(cleaned); len(runes) > maxDescriptorLength {
		cleaned = string(runes[:maxDescriptorLength]) + "…"
	}

	return cleaned
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
