package main

import (
	"testing"
)

// TestParseClientSubcommand tests the subcommand parsing from clientCmd.
func TestParseClientSubcommand_Pin(t *testing.T) {
	sub := parseClientSubcommand([]string{"pin"})
	if sub != "pin" {
		t.Errorf("subcommand = %q, want %q", sub, "pin")
	}
}

func TestParseClientSubcommand_List(t *testing.T) {
	sub := parseClientSubcommand([]string{"list"})
	if sub != "list" {
		t.Errorf("subcommand = %q, want %q", sub, "list")
	}
}

func TestParseClientSubcommand_Pending(t *testing.T) {
	sub := parseClientSubcommand([]string{"pending"})
	if sub != "pending" {
		t.Errorf("subcommand = %q, want %q", sub, "pending")
	}
}

func TestParseClientSubcommand_Remove(t *testing.T) {
	sub := parseClientSubcommand([]string{"remove"})
	if sub != "remove" {
		t.Errorf("subcommand = %q, want %q", sub, "remove")
	}
}

func TestParseClientSubcommand_Status(t *testing.T) {
	sub := parseClientSubcommand([]string{"status"})
	if sub != "status" {
		t.Errorf("subcommand = %q, want %q", sub, "status")
	}
}

func TestParseClientSubcommand_Unknown(t *testing.T) {
	sub := parseClientSubcommand([]string{"foobar"})
	if sub != "foobar" {
		t.Errorf("subcommand = %q, want %q", sub, "foobar")
	}
}

func TestParseClientSubcommand_Empty(t *testing.T) {
	sub := parseClientSubcommand([]string{})
	if sub != "" {
		t.Errorf("subcommand = %q, want empty", sub)
	}
}

func TestParseClientSubcommand_SingleArg(t *testing.T) {
	// Only one arg means no subcommand was given (os.Args[2:] would be empty)
	sub := parseClientSubcommand([]string{})
	if sub != "" {
		t.Errorf("subcommand = %q, want empty", sub)
	}
}

// TestParseClientPinArgs tests the pin argument parsing logic.
func TestParseClientPinArgs_DeviceLong(t *testing.T) {
	dev := parseClientPinArgs([]string{"--device", "My Laptop"})
	if dev != "My Laptop" {
		t.Errorf("device = %q, want %q", dev, "My Laptop")
	}
}

func TestParseClientPinArgs_DeviceShort(t *testing.T) {
	dev := parseClientPinArgs([]string{"-d", "My Laptop"})
	if dev != "My Laptop" {
		t.Errorf("device = %q, want %q", dev, "My Laptop")
	}
}

func TestParseClientPinArgs_Empty(t *testing.T) {
	dev := parseClientPinArgs([]string{})
	if dev != "" {
		t.Errorf("device = %q, want empty", dev)
	}
}

func TestParseClientPinArgs_DeviceMissingValue(t *testing.T) {
	dev := parseClientPinArgs([]string{"--device"})
	if dev != "" {
		t.Errorf("device = %q, want empty (no value to consume)", dev)
	}
}

func TestParseClientPinArgs_DeviceShortMissingValue(t *testing.T) {
	dev := parseClientPinArgs([]string{"-d"})
	if dev != "" {
		t.Errorf("device = %q, want empty (no value to consume)", dev)
	}
}

func TestParseClientPinArgs_UnknownFlagIgnored(t *testing.T) {
	dev := parseClientPinArgs([]string{"--unknown", "value"})
	if dev != "" {
		t.Errorf("device = %q, want empty", dev)
	}
}

func TestParseClientPinArgs_MultipleDevices(t *testing.T) {
	// Last device wins, like the real parser
	dev := parseClientPinArgs([]string{
		"--device", "old", "--device", "new",
	})
	if dev != "new" {
		t.Errorf("device = %q, want %q", dev, "new")
	}
}

func TestParseClientPinArgs_WithOtherFlags(t *testing.T) {
	dev := parseClientPinArgs([]string{
		"--device", "My Phone", "--other", "value",
	})
	if dev != "My Phone" {
		t.Errorf("device = %q, want %q", dev, "My Phone")
	}
}

func TestParseClientPinArgs_DeviceAfterUnknown(t *testing.T) {
	dev := parseClientPinArgs([]string{
		"--unknown", "value", "--device", "My Laptop",
	})
	if dev != "My Laptop" {
		t.Errorf("device = %q, want %q", dev, "My Laptop")
	}
}
