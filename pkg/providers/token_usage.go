package providers

import "unicode/utf8"

func EstimateTokensFromChars(chars int) int {
	return chars * 2 / 5
}

func EstimateTokensFromText(s string) int {
	return EstimateTokensFromChars(utf8.RuneCountInString(s))
}

func ResponseTokenCounts(messages []Message, response *LLMResponse) (inputTokens, outputTokens int) {
	if response == nil {
		return 0, 0
	}

	if response.Usage != nil {
		return response.Usage.PromptTokens, response.Usage.CompletionTokens
	}

	var inputChars int
	for _, msg := range messages {
		inputChars += utf8.RuneCountInString(msg.Content)
	}
	return EstimateTokensFromChars(inputChars), EstimateTokensFromText(response.Content)
}
