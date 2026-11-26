package channels

import (
	"github.com/xilistudios/lele/pkg/logger"
)

const requesterAuthorityChannel = "telegram"

func channelHasRequesterAuthority(channel string) bool {
	return channel == requesterAuthorityChannel
}

func isNumericID(id string) bool {
	if id == "" {
		return false
	}
	for _, r := range id {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func ApprovalRequesterForTurn(channel, senderID string) string {
	if !channelHasRequesterAuthority(channel) {
		return ""
	}
	if !isNumericID(requesterIDPart(senderID)) {
		logger.WarnCF("channels", "Tool approval requested without a user identity; falling back to session authority", map[string]interface{}{
			"channel":   channel,
			"sender_id": senderID,
		})
		return ""
	}
	return senderID
}
