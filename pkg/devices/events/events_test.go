package events

import "testing"

func TestFormatMessage_Add(t *testing.T) {
	ev := &DeviceEvent{
		Action:       ActionAdd,
		Kind:         KindUSB,
		DeviceID:     "1-2",
		Vendor:       "Logitech",
		Product:      "Mouse",
		Serial:       "SN123",
		Capabilities: "HID (Keyboard/Mouse/Gamepad)",
		Raw:          map[string]string{"ACTION": "add"},
	}

	got := ev.FormatMessage()
	wantSubs := []string{
		"🔌",
		"Connected",
		"Type: usb",
		"Device: Logitech Mouse",
		"Capabilities: HID (Keyboard/Mouse/Gamepad)",
		"Serial: SN123",
	}
	for _, sub := range wantSubs {
		if !contains(got, sub) {
			t.Errorf("FormatMessage() for add event missing %q\nGot:\n%s", sub, got)
		}
	}
}

func TestFormatMessage_Remove(t *testing.T) {
	ev := &DeviceEvent{
		Action:  ActionRemove,
		Kind:    KindBluetooth,
		Vendor:  "Sony",
		Product: "Headphones",
	}

	got := ev.FormatMessage()
	for _, sub := range []string{"Disconnected", "Device: Sony Headphones", "Type: bluetooth"} {
		if !contains(got, sub) {
			t.Errorf("FormatMessage() for remove event missing %q\nGot:\n%s", sub, got)
		}
	}
	if contains(got, "Capabilities:") {
		t.Errorf("FormatMessage() should omit empty capabilities\nGot:\n%s", got)
	}
	if contains(got, "Serial:") {
		t.Errorf("FormatMessage() should omit empty serial\nGot:\n%s", got)
	}
}

func TestFormatMessage_EmptyVendorProduct(t *testing.T) {
	ev := &DeviceEvent{
		Action:  ActionChange,
		Kind:    KindPCI,
		Vendor:  "",
		Product: "",
	}
	got := ev.FormatMessage()
	if !contains(got, "Device:  ") {
		t.Errorf("FormatMessage() should include empty vendor/product fields\nGot:\n%s", got)
	}
	// change action is neither add nor remove -> defaults to Connected
	if !contains(got, "Connected") {
		t.Errorf("FormatMessage() for change action should default to Connected\nGot:\n%s", got)
	}
}

func TestDeviceEventFormat_AllKinds(t *testing.T) {
	for _, kind := range []Kind{KindUSB, KindBluetooth, KindPCI, KindGeneric} {
		ev := &DeviceEvent{Action: ActionAdd, Kind: kind, Vendor: "V", Product: "P"}
		got := ev.FormatMessage()
		if !contains(got, "Type: "+string(kind)) {
			t.Errorf("FormatMessage() missing kind %q\nGot:\n%s", kind, got)
		}
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}