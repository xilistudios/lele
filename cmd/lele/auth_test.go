package main

import (
	"testing"
)

// TestParseAuthSubcommand tests the subcommand parsing from authCmd.
func TestParseAuthSubcommand_Login(t *testing.T) {
	sub := parseAuthSubcommand([]string{"login"})
	if sub != "login" {
		t.Errorf("subcommand = %q, want %q", sub, "login")
	}
}

func TestParseAuthSubcommand_Logout(t *testing.T) {
	sub := parseAuthSubcommand([]string{"logout"})
	if sub != "logout" {
		t.Errorf("subcommand = %q, want %q", sub, "logout")
	}
}

func TestParseAuthSubcommand_Status(t *testing.T) {
	sub := parseAuthSubcommand([]string{"status"})
	if sub != "status" {
		t.Errorf("subcommand = %q, want %q", sub, "status")
	}
}

func TestParseAuthSubcommand_Unknown(t *testing.T) {
	sub := parseAuthSubcommand([]string{"foobar"})
	if sub != "foobar" {
		t.Errorf("subcommand = %q, want %q", sub, "foobar")
	}
}

func TestParseAuthSubcommand_Empty(t *testing.T) {
	sub := parseAuthSubcommand([]string{})
	if sub != "" {
		t.Errorf("subcommand = %q, want empty", sub)
	}
}

// TestParseLoginArgs tests the login argument parsing logic.
func TestParseLoginArgs_ProviderLong(t *testing.T) {
	provider, dev := parseLoginArgs([]string{"--provider", "openai"})
	if provider != "openai" {
		t.Errorf("provider = %q, want %q", provider, "openai")
	}
	if dev {
		t.Error("useDeviceCode = true, want false")
	}
}

func TestParseLoginArgs_ProviderShort(t *testing.T) {
	provider, _ := parseLoginArgs([]string{"-p", "anthropic"})
	if provider != "anthropic" {
		t.Errorf("provider = %q, want %q", provider, "anthropic")
	}
}

func TestParseLoginArgs_DeviceCode(t *testing.T) {
	_, dev := parseLoginArgs([]string{"--device-code"})
	if !dev {
		t.Error("useDeviceCode = false, want true")
	}
}

func TestParseLoginArgs_DeviceCodeWithProvider(t *testing.T) {
	provider, dev := parseLoginArgs([]string{
		"--provider", "openai", "--device-code",
	})
	if provider != "openai" {
		t.Errorf("provider = %q, want %q", provider, "openai")
	}
	if !dev {
		t.Error("useDeviceCode = false, want true")
	}
}

func TestParseLoginArgs_AllFlags(t *testing.T) {
	provider, dev := parseLoginArgs([]string{
		"--provider", "openai", "--device-code",
	})
	if provider != "openai" {
		t.Errorf("provider = %q, want %q", provider, "openai")
	}
	if !dev {
		t.Error("useDeviceCode = false, want true")
	}
}

func TestParseLoginArgs_Empty(t *testing.T) {
	provider, dev := parseLoginArgs([]string{})
	if provider != "" {
		t.Errorf("provider = %q, want empty", provider)
	}
	if dev {
		t.Error("useDeviceCode = true, want false")
	}
}

func TestParseLoginArgs_ProviderMissingValue(t *testing.T) {
	provider, _ := parseLoginArgs([]string{"--provider"})
	if provider != "" {
		t.Errorf("provider = %q, want empty (no value to consume)", provider)
	}
}

func TestParseLoginArgs_ProviderShortMissingValue(t *testing.T) {
	provider, _ := parseLoginArgs([]string{"-p"})
	if provider != "" {
		t.Errorf("provider = %q, want empty (no value to consume)", provider)
	}
}

func TestParseLoginArgs_MultipleProviders(t *testing.T) {
	// Last provider wins, like the real parser
	provider, _ := parseLoginArgs([]string{
		"--provider", "openai", "--provider", "anthropic",
	})
	if provider != "anthropic" {
		t.Errorf("provider = %q, want %q", provider, "anthropic")
	}
}

func TestParseLoginArgs_DeviceCodeBeforeProvider(t *testing.T) {
	provider, dev := parseLoginArgs([]string{
		"--device-code", "--provider", "openai",
	})
	if provider != "openai" {
		t.Errorf("provider = %q, want %q", provider, "openai")
	}
	if !dev {
		t.Error("useDeviceCode = false, want true")
	}
}

func TestParseLoginArgs_UnknownFlagsIgnored(t *testing.T) {
	provider, dev := parseLoginArgs([]string{"--unknown", "value"})
	if provider != "" {
		t.Errorf("provider = %q, want empty", provider)
	}
	if dev {
		t.Error("useDeviceCode = true, want false")
	}
}

// TestParseLogoutArgs tests the logout argument parsing logic.
func TestParseLogoutArgs_ProviderLong(t *testing.T) {
	provider := parseLogoutArgs([]string{"--provider", "openai"})
	if provider != "openai" {
		t.Errorf("provider = %q, want %q", provider, "openai")
	}
}

func TestParseLogoutArgs_ProviderShort(t *testing.T) {
	provider := parseLogoutArgs([]string{"-p", "anthropic"})
	if provider != "anthropic" {
		t.Errorf("provider = %q, want %q", provider, "anthropic")
	}
}

func TestParseLogoutArgs_Empty(t *testing.T) {
	provider := parseLogoutArgs([]string{})
	if provider != "" {
		t.Errorf("provider = %q, want empty", provider)
	}
}

func TestParseLogoutArgs_ProviderMissingValue(t *testing.T) {
	provider := parseLogoutArgs([]string{"--provider"})
	if provider != "" {
		t.Errorf("provider = %q, want empty (no value to consume)", provider)
	}
}

func TestParseLogoutArgs_ProviderShortMissingValue(t *testing.T) {
	provider := parseLogoutArgs([]string{"-p"})
	if provider != "" {
		t.Errorf("provider = %q, want empty (no value to consume)", provider)
	}
}

func TestParseLogoutArgs_UnknownFlagIgnored(t *testing.T) {
	provider := parseLogoutArgs([]string{"--unknown", "value"})
	if provider != "" {
		t.Errorf("provider = %q, want empty", provider)
	}
}
