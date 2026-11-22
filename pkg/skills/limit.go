package skills

import (
	"fmt"
	"io"
	"net/http"
)

const (
	MaxSkillBodyBytes = 5 << 20
)

func readLimitedBody(resp *http.Response) ([]byte, error) {
	limited := io.LimitReader(resp.Body, MaxSkillBodyBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}
	if len(data) > MaxSkillBodyBytes {
		return nil, fmt.Errorf("response body exceeds %d bytes limit", MaxSkillBodyBytes)
	}
	return data, nil
}
