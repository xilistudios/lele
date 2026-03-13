package tools

import (
	"context"
	"encoding/base64"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/sipeed/picoclaw/pkg/providers"
)

const maxImageReadSize = 20 << 20

type ReadImageTool struct {
	workspace string
	restrict  bool
}

func NewReadImageTool(workspace string, restrict bool) *ReadImageTool {
	return &ReadImageTool{workspace: workspace, restrict: restrict}
}

func (t *ReadImageTool) Name() string {
	return "read_image"
}

func (t *ReadImageTool) Description() string {
	return "Read an image file and add it to the LLM context as OpenAI-compatible multimodal content"
}

func (t *ReadImageTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{
				"type":        "string",
				"description": "Path to the image file to read",
			},
			"prompt": map[string]interface{}{
				"type":        "string",
				"description": "Optional text to send alongside the image for analysis",
			},
			"detail": map[string]interface{}{
				"type":        "string",
				"description": "Optional image detail level: auto, low, or high",
				"enum":        []string{"auto", "low", "high"},
			},
		},
		"required": []string{"path"},
	}
}

func (t *ReadImageTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	path, ok := args["path"].(string)
	if !ok || strings.TrimSpace(path) == "" {
		return ErrorResult("path is required")
	}

	resolvedPath, err := validatePath(path, t.workspace, t.restrict)
	if err != nil {
		return ErrorResult(err.Error())
	}

	fileInfo, err := os.Stat(resolvedPath)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to stat image: %v", err))
	}
	if fileInfo.IsDir() {
		return ErrorResult("path must be an image file, not a directory")
	}
	if fileInfo.Size() > maxImageReadSize {
		return ErrorResult(fmt.Sprintf("image is too large: %d bytes exceeds %d byte limit", fileInfo.Size(), maxImageReadSize))
	}

	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to read image: %v", err))
	}

	mimeType := detectImageMIME(resolvedPath, data)
	if !isSupportedImageMIME(mimeType) {
		return ErrorResult(fmt.Sprintf("unsupported image type: %s", mimeType))
	}

	prompt := strings.TrimSpace(asString(args["prompt"]))
	if prompt == "" {
		prompt = fmt.Sprintf("Analyze the image at %s.", path)
	}

	detail := strings.TrimSpace(asString(args["detail"]))
	if detail == "" {
		detail = "auto"
	}
	if detail != "auto" && detail != "low" && detail != "high" {
		return ErrorResult("detail must be one of: auto, low, high")
	}

	encoded := base64.StdEncoding.EncodeToString(data)
	imageURL := fmt.Sprintf("data:%s;base64,%s", mimeType, encoded)

	contextMsg := providers.Message{
		Role: "user",
		ContentParts: []providers.ContentPart{
			{Type: "text", Text: prompt},
			{Type: "image_url", ImageURL: &providers.ImageURL{URL: imageURL, Detail: detail}},
		},
	}

	return &ToolResult{
		ForLLM: fmt.Sprintf("Image loaded into context from %s (%s, %d bytes)", path, mimeType, len(data)),
		Silent: true,
		ContextMessages: []providers.Message{
			contextMsg,
		},
	}
}

func detectImageMIME(path string, data []byte) string {
	if len(data) > 0 {
		sniffLen := len(data)
		if sniffLen > 512 {
			sniffLen = 512
		}
		mimeType := http.DetectContentType(data[:sniffLen])
		if isSupportedImageMIME(mimeType) {
			return mimeType
		}
	}

	if ext := strings.ToLower(filepath.Ext(path)); ext != "" {
		mimeType := mime.TypeByExtension(ext)
		if idx := strings.Index(mimeType, ";"); idx >= 0 {
			mimeType = mimeType[:idx]
		}
		if isSupportedImageMIME(mimeType) {
			return mimeType
		}
	}

	return "application/octet-stream"
}

func isSupportedImageMIME(mimeType string) bool {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}

func asString(v interface{}) string {
	s, _ := v.(string)
	return s
}
