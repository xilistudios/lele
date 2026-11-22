package utils

import (
	"fmt"
	"math/rand"
	"time"
	"unicode/utf8"
)

func Truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return string(runes[:maxLen])
	}
	return string(runes[:maxLen-3]) + "..."
}

func TruncateOutput(s string, headBytes, tailBytes int) string {
	if len(s) <= headBytes+tailBytes {
		return s
	}

	headCut := headBytes
	for headCut > 0 && !utf8.RuneStart(s[headCut]) {
		headCut--
	}

	tailStart := len(s) - tailBytes
	for tailStart < len(s) && !utf8.RuneStart(s[tailStart]) {
		tailStart++
	}

	if headCut >= tailStart {
		return s[:headCut] + fmt.Sprintf("\n[... %d bytes truncated ...]", len(s)-headCut)
	}

	dropped := len(s) - headCut - (len(s) - tailStart)
	marker := fmt.Sprintf("\n[... %d bytes truncated ...]\n", dropped)
	return s[:headCut] + marker + s[tailStart:]
}

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

func RandomProcessName() string {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	adj := adjectives[rng.Intn(len(adjectives))]
	noun := nouns[rng.Intn(len(nouns))]
	return adj + "-" + noun
}

func RandomProcessNameWithEmoji() string {
	emojis := []string{"🧰", "⚡", "🔧", "⚙️", "🛠️", "🔨", "📦", "🚀", "💡", "🔍"}
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	emoji := emojis[rng.Intn(len(emojis))]
	return emoji + " Process: " + RandomProcessName()
}
