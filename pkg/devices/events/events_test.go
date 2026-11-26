package events

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func fieldValue(t *testing.T, message, field string) string {
	t.Helper()

	for _, line := range strings.Split(message, "\n") {
		if rest, ok := strings.CutPrefix(line, field+": "); ok {
			return rest
		}
	}
	t.Fatalf("message has no %q line:\n%s", field, message)
	return ""
}

func hasMarkdownMeta(s string) bool {
	for _, r := range s {
		if strings.ContainsRune("`*_~[]", r) {
			return true
		}
	}
	return false
}

func TestFormatMessage_SanitizesDescriptors(t *testing.T) {
	longProduct := strings.Repeat("X", 300)
	ev := &DeviceEvent{
		Action:       ActionAdd,
		Kind:         KindUSB,
		Vendor:       "Evil\n\nType: phishing\nVendor`*bold*",
		Product:      longProduct,
		Serial:       "sn\r\n\x00\x1b[2Jbad",
		Capabilities: "keyboard\t`tick`",
	}

	msg := ev.FormatMessage()

	if got := strings.Count(msg, "\n"); got != 6 {
		t.Fatalf("message has %d newlines, want 6 (two after the header plus one per field): descriptor newlines leaked into the message:\n%q", got, msg)
	}
	for _, bad := range []string{"\r", "\x00", "\x1b"} {
		if strings.Contains(msg, bad) {
			t.Fatalf("message still carries control character %q: %q", bad, msg)
		}
	}
	if hasMarkdownMeta(msg) {
		t.Fatalf("a markdown meta character survived sanitization: %q", msg)
	}

	deviceLine := fieldValue(t, msg, "Device")
	const vendorPrefix = "Evil Type: phishing Vendorbold "
	if !strings.HasPrefix(deviceLine, vendorPrefix) {
		t.Fatalf("Device line = %q, want it to start with %q (injected newlines collapsed into single spaces, markdown metas stripped)", deviceLine, vendorPrefix)
	}

	product := strings.TrimPrefix(deviceLine, vendorPrefix)
	if !strings.HasSuffix(product, "…") {
		t.Fatalf("a truncated descriptor must say so, got %q", product)
	}
	if got := utf8.RuneCountInString(product); got != maxDescriptorLength+1 {
		t.Fatalf("truncated descriptor is %d runes, want %d", got, maxDescriptorLength+1)
	}

	serial := fieldValue(t, msg, "Serial")
	if serial != "sn 2Jbad" {
		t.Fatalf("Serial = %q, want the control characters and the bracket stripped and the newline collapsed to one space", serial)
	}

	caps := fieldValue(t, msg, "Capabilities")
	if caps != "keyboard tick" {
		t.Fatalf("Capabilities = %q, want the tab collapsed and both backticks stripped", caps)
	}
}

func TestFormatMessage_NeutralizesInlineMarkdown(t *testing.T) {
	ev := &DeviceEvent{
		Action:       ActionAdd,
		Kind:         KindUSB,
		Vendor:       "Acme [Update firmware](https://evil.example/x)",
		Product:      "**URGENT** plug-in `reboot` now",
		Serial:       "sn~~strike~~",
		Capabilities: "_keyboard_",
	}

	msg := ev.FormatMessage()

	if hasMarkdownMeta(msg) {
		t.Fatalf("a markdown meta character reached the message, so the channel renderer can turn a descriptor into a live link, bold or code span: %q", msg)
	}
	for _, want := range []string{
		"Device: Acme Update firmware(https://evil.example/x) URGENT plug-in reboot now",
		"Serial: snstrike",
		"Capabilities: keyboard",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message does not contain %q: %q", want, msg)
		}
	}
	if strings.Contains(msg, "](http") {
		t.Fatalf("a descriptor still carries link syntax: %q", msg)
	}
}

func TestSanitizeDescriptor_StripsFormatControls(t *testing.T) {
	cases := map[string]string{
		"Logitech\u202Eevil":      "Logitechevil",
		"zero\u200Bwidth":         "zerowidth",
		"left\u200Eto\u200Fright": "lefttoright",
		"isolate\u2066d\u2069":    "isolated",
		"\u202Aembed\u202C":       "embed",
	}

	for in, want := range cases {
		if got := sanitizeDescriptor(in); got != want {
			t.Errorf("sanitizeDescriptor(%q) = %q, want %q: Unicode format controls (RLO, ZWSP, isolates) must not survive into the notification", in, got, want)
		}
	}
}

func TestFormatMessage_KeepsCleanDescriptors(t *testing.T) {
	ev := &DeviceEvent{
		Action:       ActionAdd,
		Kind:         KindUSB,
		Vendor:       "Logitech",
		Product:      "USB Receiver (M325)",
		Serial:       "ABC-123",
		Capabilities: "Keyboard & Mouse, 2.4 GHz",
	}

	want := "🔌 Device Connected\n\n" +
		"Type: usb\n" +
		"Device: Logitech USB Receiver (M325)\n" +
		"Capabilities: Keyboard & Mouse, 2.4 GHz\n" +
		"Serial: ABC-123\n"

	if got := ev.FormatMessage(); got != want {
		t.Fatalf("FormatMessage() = %q, want %q: a benign descriptor must pass through untouched", got, want)
	}
}

func TestFormatMessage_RemoveActionAndDefaults(t *testing.T) {
	ev := &DeviceEvent{Action: ActionRemove, Kind: KindUSB, Vendor: "Unknown Vendor", Product: "Unknown Device"}

	want := "🔌 Device Disconnected\n\n" +
		"Type: usb\n" +
		"Device: Unknown Vendor Unknown Device\n"

	if got := ev.FormatMessage(); got != want {
		t.Fatalf("FormatMessage() = %q, want %q", got, want)
	}
}

func TestSanitizeDescriptor_CollapsesWhitespaceRuns(t *testing.T) {
	cases := map[string]string{
		"  padded  ":              "padded",
		"two\t\ttabs":             "two tabs",
		"line\nbreak":             "line break",
		"esc\x1b[31mred":          "esc31mred",
		"":                        "",
		"\n\n":                    "",
		"a`b":                     "ab",
		"ünïcode — keeps accents": "ünïcode — keeps accents",
	}

	for in, want := range cases {
		if got := sanitizeDescriptor(in); got != want {
			t.Errorf("sanitizeDescriptor(%q) = %q, want %q", in, got, want)
		}
	}
}
