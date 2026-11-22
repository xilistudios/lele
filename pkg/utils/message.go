package utils

import (
	"strings"
)

func SplitMessage(content string, maxLen int) []string {
	var messages []string

	codeBlockBuffer := maxLen / 10
	if codeBlockBuffer < 50 {
		codeBlockBuffer = 50
	}
	if codeBlockBuffer > maxLen/2 {
		codeBlockBuffer = maxLen / 2
	}

	for len(content) > 0 {
		if len(content) <= maxLen {
			messages = append(messages, content)
			break
		}

		effectiveLimit := maxLen - codeBlockBuffer
		if effectiveLimit < maxLen/2 {
			effectiveLimit = maxLen / 2
		}

		msgEnd := findLastNewline(content[:effectiveLimit], 200)
		if msgEnd <= 0 {
			msgEnd = findLastSpace(content[:effectiveLimit], 100)
		}
		if msgEnd <= 0 {
			msgEnd = effectiveLimit
		}

		candidate := content[:msgEnd]
		unclosedIdx := findLastUnclosedCodeBlock(candidate)

		if unclosedIdx >= 0 {
			if len(content) > msgEnd {
				closingIdx := findNextClosingCodeBlock(content, msgEnd)
				if closingIdx > 0 && closingIdx <= maxLen {
					msgEnd = closingIdx
				} else {
					headerEnd := strings.Index(content[unclosedIdx:], "\n")
					if headerEnd == -1 {
						headerEnd = unclosedIdx + 3
					} else {
						headerEnd += unclosedIdx
					}
					header := strings.TrimSpace(content[unclosedIdx:headerEnd])

					if msgEnd > headerEnd+20 {
						innerLimit := maxLen - 5
						betterEnd := findLastNewline(content[:innerLimit], 200)
						if betterEnd > headerEnd {
							msgEnd = betterEnd
						} else {
							msgEnd = innerLimit
						}
						messages = append(messages, strings.TrimRight(content[:msgEnd], " \t\n\r")+"\n```")
						content = strings.TrimSpace(header + "\n" + content[msgEnd:])
						continue
					}

					newEnd := findLastNewline(content[:unclosedIdx], 200)
					if newEnd <= 0 {
						newEnd = findLastSpace(content[:unclosedIdx], 100)
					}
					if newEnd > 0 {
						msgEnd = newEnd
					} else {
						if unclosedIdx > 20 {
							msgEnd = unclosedIdx
						} else {
							msgEnd = maxLen - 5
							messages = append(messages, strings.TrimRight(content[:msgEnd], " \t\n\r")+"\n```")
							content = strings.TrimSpace(header + "\n" + content[msgEnd:])
							continue
						}
					}
				}
			}
		}

		if msgEnd <= 0 {
			msgEnd = effectiveLimit
		}

		messages = append(messages, content[:msgEnd])
		content = strings.TrimSpace(content[msgEnd:])
	}

	return messages
}

func findLastUnclosedCodeBlock(text string) int {
	inCodeBlock := false
	lastOpenIdx := -1

	for i := 0; i < len(text); i++ {
		if i+2 < len(text) && text[i] == '`' && text[i+1] == '`' && text[i+2] == '`' {
			if !inCodeBlock {
				lastOpenIdx = i
			}
			inCodeBlock = !inCodeBlock
			i += 2
		}
	}

	if inCodeBlock {
		return lastOpenIdx
	}
	return -1
}

func findNextClosingCodeBlock(text string, startIdx int) int {
	for i := startIdx; i < len(text); i++ {
		if i+2 < len(text) && text[i] == '`' && text[i+1] == '`' && text[i+2] == '`' {
			return i + 3
		}
	}
	return -1
}

func findLastNewline(s string, searchWindow int) int {
	searchStart := len(s) - searchWindow
	if searchStart < 0 {
		searchStart = 0
	}
	for i := len(s) - 1; i >= searchStart; i-- {
		if s[i] == '\n' {
			return i
		}
	}
	return -1
}

func findLastSpace(s string, searchWindow int) int {
	searchStart := len(s) - searchWindow
	if searchStart < 0 {
		searchStart = 0
	}
	for i := len(s) - 1; i >= searchStart; i-- {
		if s[i] == ' ' || s[i] == '\t' {
			return i
		}
	}
	return -1
}
