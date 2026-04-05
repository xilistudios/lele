package utils

import (
	"math/rand"
	"time"
)

// Truncate returns a truncated version of s with at most maxLen runes.
// Handles multi-byte Unicode characters properly.
// If the string is truncated, "..." is appended to indicate truncation.
func Truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	// Reserve 3 chars for "..."
	if maxLen <= 3 {
		return string(runes[:maxLen])
	}
	return string(runes[:maxLen-3]) + "..."
}

// adjectives and nouns for generating random process names
var (
	adjectives = []string{
		"brave", "calm", "eager", "fancy", "gentle", "happy", "jolly", "kind",
		"lively", "merry", "nice", "proud", "silly", "witty", "zealous", "bold",
		"bright", "busy", "chilly", "cozy", "daring", "fierce", "grand", "heavy",
		"keen", "mighty", "noble", "quick", "rapid", "sharp", "swift", "tough",
		"vivid", "wild", "young", "agile", "fuzzy", "sunny", "stormy", "dusty",
		"crimson", "golden", "silver", "bronze", "azure", "amber", "jade", "ruby",
		"azure", "cosmic", "stellar", "lunar", "solar", "nebula", "quantum", "atomic",
		"brave", "clever", "mystic", "magic", "epic", "legend", "cosmic", "galactic",
	}

	nouns = []string{
		"claw", "fang", "wing", "tail", "paw", "horn", "mane", "scale",
		"beak", "talon", "hoof", "fin", "spike", "quill", "antler", "tusk",
		"blade", "shield", "arrow", "spear", "dagger", "sword", "hammer", "axe",
		"stone", "flame", "frost", "storm", "thunder", "shadow", "spirit", "soul",
		"heart", "mind", "dream", "star", "moon", "sun", "comet", "nova",
		"fox", "wolf", "bear", "hawk", "eagle", "owl", "raven", "crow",
		"lion", "tiger", "lynx", "puma", "falcon", "dragon", "phoenix", "griffin",
		"wave", "tide", "current", "drift", "spark", "ember", "cinder", "ash",
	}
)

// RandomProcessName generates a random process name in the format "adjective-noun"
// Example: "grand-claw", "swift-fox", "cosmic-dragon"
func RandomProcessName() string {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	adj := adjectives[rng.Intn(len(adjectives))]
	noun := nouns[rng.Intn(len(nouns))]
	return adj + "-" + noun
}

// RandomProcessNameWithEmoji generates a random process name with an emoji prefix
// Example: "🧰 grand-claw", "⚡ swift-fox"
func RandomProcessNameWithEmoji() string {
	emojis := []string{"🧰", "⚡", "🔧", "⚙️", "🛠️", "🔨", "📦", "🚀", "💡", "🔍"}
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	emoji := emojis[rng.Intn(len(emojis))]
	return emoji + " Process: " + RandomProcessName()
}
