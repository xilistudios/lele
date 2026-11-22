package channels

func hasSendablePayload[T any](content string, attachments []T) bool {
	return content != "" || len(attachments) > 0
}
