package channels

import (
	"reflect"
	"testing"
)

func TestTelegramMenuCommands(t *testing.T) {
	commands := telegramMenuCommands(telegramCommandRegistry)
	got := make([]string, 0, len(commands))
	for _, command := range commands {
		got = append(got, command.Command)
		if command.Description == "" {
			t.Fatalf("command %s should have a description", command.Command)
		}
	}

	want := []string{"models", "new", "stop", "model", "status", "compact", "subagents", "toggle", "verbose", "think", "agent"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected command menu order: got %v want %v", got, want)
	}
}
