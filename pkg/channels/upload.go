package channels

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/utils"
)

func (n *NativeChannel) handleFileUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "method_invalid")
		return
	}

	clientID := getClientID(r)

	maxSize := n.cfg.MaxUploadSizeMB * 1024 * 1024
	if err := r.ParseMultipartForm(maxSize); err != nil {
		if err.Error() == "http: request body too large" {
			writeError(w, http.StatusRequestEntityTooLarge,
				"file too large (max "+strconv.FormatInt(n.cfg.MaxUploadSizeMB, 10)+"MB)",
				"file_too_large")
		} else {
			writeError(w, http.StatusBadRequest, "invalid multipart form", "form_invalid")
		}
		return
	}

	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		writeError(w, http.StatusBadRequest, "no files provided", "files_missing")
		return
	}

	uploadDir := filepath.Join(n.cfg.LeleDir, "tmp", "uploads")
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		writeError(w, http.StatusInternalServerError,
			"failed to create upload directory", "dir_error")
		return
	}

	uploadedFiles := make([]UploadedFile, 0, len(files))

	for _, header := range files {
		file, err := header.Open()
		if err != nil {
			logger.WarnCF("upload", "Failed to open uploaded file",
				map[string]interface{}{"error": err.Error()})
			continue
		}

		id := uuid.New().String()[:8]
		originalName := filepath.Base(header.Filename)
		sanitizedName := utils.SanitizeFilename(originalName)
		if sanitizedName == "" || sanitizedName == "." {
			sanitizedName = "attachment"
		}

		uniqueName := id + "_" + sanitizedName
		destPath := filepath.Join(uploadDir, uniqueName)

		destFile, err := os.Create(destPath)
		if err != nil {
			file.Close()
			logger.WarnCF("upload", "Failed to create destination file",
				map[string]interface{}{"error": err.Error()})
			continue
		}

		copied, err := io.Copy(destFile, file)
		file.Close()
		destFile.Close()

		if err != nil {
			os.Remove(destPath)
			logger.WarnCF("upload", "Failed to save uploaded file",
				map[string]interface{}{"error": err.Error()})
			continue
		}

		mimeType := detectMimeType(destPath)

		uploadedFiles = append(uploadedFiles, UploadedFile{
			ID:       id,
			Path:     destPath,
			Name:     originalName,
			MIMEType: mimeType,
			Size:     copied,
		})

		logger.InfoCF("upload", "File uploaded successfully",
			map[string]interface{}{
				"client_id": clientID,
				"file_id":   id,
				"name":      originalName,
				"size":      copied,
				"mime_type": mimeType,
			})
	}

	if len(uploadedFiles) == 0 {
		writeError(w, http.StatusBadRequest, "all files failed to upload", "upload_failed")
		return
	}

	writeJSON(w, http.StatusOK, FileUploadResponse{Files: uploadedFiles})
}

func detectMimeType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))

	mimeTypes := map[string]string{
		".png":  "image/png",
		".jpg":  "image/jpeg",
		".jpeg": "image/jpeg",
		".gif":  "image/gif",
		".webp": "image/webp",
		".pdf":  "application/pdf",
		".txt":  "text/plain",
		".md":   "text/markdown",
		".csv":  "text/csv",
		".json": "application/json",
		".xml":  "application/xml",
		".zip":  "application/zip",
		".mp3":  "audio/mpeg",
		".mp4":  "video/mp4",
		".avi":  "video/x-msvideo",
		".doc":  "application/msword",
		".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		".xls":  "application/vnd.ms-excel",
		".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	}

	if mt, ok := mimeTypes[ext]; ok {
		return mt
	}

	file, err := os.Open(path)
	if err != nil {
		return "application/octet-stream"
	}
	defer file.Close()

	buffer := make([]byte, 512)
	n, err := file.Read(buffer)
	if err != nil {
		return "application/octet-stream"
	}

	return http.DetectContentType(buffer[:n])
}
