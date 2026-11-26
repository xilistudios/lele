package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/utils"
)

const (
	defaultGroqAPIBase = "https://api.groq.com/openai/v1"
	maxResponseBytes   = 10 << 20
	maxErrorBodyRunes  = 512
)

type GroqTranscriber struct {
	apiKey     string
	apiBase    string
	httpClient *http.Client
}

type TranscriptionResponse struct {
	Text     string  `json:"text"`
	Language string  `json:"language,omitempty"`
	Duration float64 `json:"duration,omitempty"`
}

// NewGroqTranscriber builds a transcriber for the Groq transcription API. A
// non-blank apiBase overrides the endpoint (a self-hosted or proxied OpenAI
// compatible Whisper server), which is what providers.groq.api_base feeds.
func NewGroqTranscriber(apiKey, apiBase string) *GroqTranscriber {
	logger.DebugCF("voice", "Creating Groq transcriber", map[string]interface{}{"has_api_key": apiKey != ""})

	apiBase = strings.TrimRight(strings.TrimSpace(apiBase), "/")
	if apiBase == "" {
		apiBase = defaultGroqAPIBase
	}
	return &GroqTranscriber{
		apiKey:  apiKey,
		apiBase: apiBase,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// readCappedBody reads an HTTP body while refusing to grow past
// maxResponseBytes: the read stops one byte over the cap instead of buffering
// whatever a broken or hostile endpoint decided to send.
func readCappedBody(r io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}
	if len(body) > maxResponseBytes {
		return nil, fmt.Errorf("response too large: more than %d bytes", maxResponseBytes)
	}
	return body, nil
}

func (t *GroqTranscriber) Transcribe(ctx context.Context, audioFilePath string) (*TranscriptionResponse, error) {
	logger.InfoCF("voice", "Starting transcription", map[string]interface{}{"audio_file": audioFilePath})

	audioFile, err := os.Open(audioFilePath)
	if err != nil {
		logger.ErrorCF("voice", "Failed to open audio file", map[string]interface{}{"path": audioFilePath, "error": err})
		return nil, fmt.Errorf("failed to open audio file: %w", err)
	}
	defer audioFile.Close()

	fileInfo, err := audioFile.Stat()
	if err != nil {
		logger.ErrorCF("voice", "Failed to get file info", map[string]interface{}{"path": audioFilePath, "error": err})
		return nil, fmt.Errorf("failed to get file info: %w", err)
	}

	logger.DebugCF("voice", "Audio file details", map[string]interface{}{
		"size_bytes": fileInfo.Size(),
		"file_name":  filepath.Base(audioFilePath),
	})

	var requestBody bytes.Buffer
	writer := multipart.NewWriter(&requestBody)

	part, err := writer.CreateFormFile("file", filepath.Base(audioFilePath))
	if err != nil {
		logger.ErrorCF("voice", "Failed to create form file", map[string]interface{}{"error": err})
		return nil, fmt.Errorf("failed to create form file: %w", err)
	}

	copied, err := io.Copy(part, audioFile)
	if err != nil {
		logger.ErrorCF("voice", "Failed to copy file content", map[string]interface{}{"error": err})
		return nil, fmt.Errorf("failed to copy file content: %w", err)
	}

	logger.DebugCF("voice", "File copied to request", map[string]interface{}{"bytes_copied": copied})

	if err := writer.WriteField("model", "whisper-large-v3"); err != nil {
		logger.ErrorCF("voice", "Failed to write model field", map[string]interface{}{"error": err})
		return nil, fmt.Errorf("failed to write model field: %w", err)
	}

	if err := writer.WriteField("response_format", "json"); err != nil {
		logger.ErrorCF("voice", "Failed to write response_format field", map[string]interface{}{"error": err})
		return nil, fmt.Errorf("failed to write response_format field: %w", err)
	}

	if err := writer.Close(); err != nil {
		logger.ErrorCF("voice", "Failed to close multipart writer", map[string]interface{}{"error": err})
		return nil, fmt.Errorf("failed to close multipart writer: %w", err)
	}

	url := t.apiBase + "/audio/transcriptions"
	req, err := http.NewRequestWithContext(ctx, "POST", url, &requestBody)
	if err != nil {
		logger.ErrorCF("voice", "Failed to create request", map[string]interface{}{"error": err})
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+t.apiKey)

	logger.DebugCF("voice", "Sending transcription request to Groq API", map[string]interface{}{
		"url":                url,
		"request_size_bytes": requestBody.Len(),
		"file_size_bytes":    fileInfo.Size(),
	})

	resp, err := t.httpClient.Do(req)
	if err != nil {
		logger.ErrorCF("voice", "Failed to send request", map[string]interface{}{"error": err})
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := readCappedBody(resp.Body)
	if err != nil {
		logger.ErrorCF("voice", "Failed to read response", map[string]interface{}{"error": err})
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		preview := utils.Truncate(string(body), maxErrorBodyRunes)
		logger.ErrorCF("voice", "API error", map[string]interface{}{
			"status_code":  resp.StatusCode,
			"response":     preview,
			"body_bytes":   len(body),
			"body_omitted": utf8.RuneCount(body) > maxErrorBodyRunes,
		})
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, preview)
	}

	logger.DebugCF("voice", "Received response from Groq API", map[string]interface{}{
		"status_code":         resp.StatusCode,
		"response_size_bytes": len(body),
	})

	var result TranscriptionResponse
	if err := json.Unmarshal(body, &result); err != nil {
		logger.ErrorCF("voice", "Failed to unmarshal response", map[string]interface{}{"error": err})
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	logger.InfoCF("voice", "Transcription completed successfully", map[string]interface{}{
		"text_length":           len(result.Text),
		"language":              result.Language,
		"duration_seconds":      result.Duration,
		"transcription_preview": utils.Truncate(result.Text, 50),
	})

	return &result, nil
}

func (t *GroqTranscriber) IsAvailable() bool {
	available := t.apiKey != ""
	logger.DebugCF("voice", "Checking transcriber availability", map[string]interface{}{"available": available})
	return available
}
