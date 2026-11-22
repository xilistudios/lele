package channels

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/utils"
)

func (n *NativeChannel) handleFileUpload(w http.ResponseWriter, r *http.Request) {
	clientID := getClientID(r)

	maxSize := n.cfg.MaxUploadSizeMB * 1024 * 1024
	r.Body = http.MaxBytesReader(w, r.Body, maxSize)
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

func publicStagingDirs(leleDir string) []string {
	return []string{
		filepath.Join(leleDir, "tmp", "attachments"),
		filepath.Join(leleDir, "tmp", "uploads"),
	}
}

func isUnderAnyPublicStagingDir(absPath string, leleDir string) bool {
	for _, dir := range publicStagingDirs(leleDir) {
		if isContainedIn(absPath, dir) {
			return true
		}
	}
	return false
}

func attachmentStagingDir(leleDir string) string {
	return filepath.Join(leleDir, "tmp", "attachments")
}

func randomHex() string {
	return uuid.New().String()[:8]
}

func stageAttachment(a *bus.FileAttachment, leleDir string) error {
	if a == nil || a.Path == "" {
		return fmt.Errorf("attachment has empty path")
	}

	leleDirAbs, err := filepath.Abs(leleDir)
	if err != nil || leleDirAbs == "" {
		return fmt.Errorf("resolve lele dir: %v", err)
	}

	absPath, err := filepath.Abs(a.Path)
	if err != nil {
		return fmt.Errorf("resolve attachment path %q: %w", a.Path, err)
	}

	origName := filepath.Base(absPath)

	realPath := absPath
	if resolved, err := filepath.EvalSymlinks(absPath); err == nil {
		realPath = resolved
	}

	stagingAbs := attachmentStagingDir(leleDirAbs)
	if isContainedIn(realPath, stagingAbs) {
		return nil
	}

	info, err := os.Stat(realPath)
	if err != nil {
		return fmt.Errorf("stat attachment %q: %w", a.Path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("attachment %q is a directory", a.Path)
	}

	dir := attachmentStagingDir(leleDirAbs)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create staging dir: %w", err)
	}

	sanitized := utils.SanitizeFilename(origName)
	if sanitized == "" || sanitized == "." {
		sanitized = "attachment"
	}
	destPath := filepath.Join(dir, randomHex()+"_"+sanitized)

	if err := copyFileAtomic(realPath, destPath); err != nil {
		return fmt.Errorf("stage attachment %q: %w", a.Path, err)
	}

	a.Name = origName
	a.Path = destPath
	return nil
}

func copyFileAtomic(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	tmp, err := os.CreateTemp(filepath.Dir(dst), ".staging-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	cleanup := func() {
		tmp.Close()
		os.Remove(tmpName)
	}

	if _, err := io.Copy(tmp, in); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0644); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, dst); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

func stageAttachments(list []bus.FileAttachment, leleDir string) []bus.FileAttachment {
	if leleDir == "" {
		return list
	}
	for i := range list {
		if err := stageAttachment(&list[i], leleDir); err != nil {
			logger.WarnCF("upload", "Failed to stage attachment for download",
				map[string]interface{}{
					"path":  list[i].Path,
					"name":  list[i].Name,
					"error": err.Error(),
				})
		}
	}
	return list
}

func attachmentsToProviders(list []bus.FileAttachment) []providers.MessageAttachment {
	if len(list) == 0 {
		return nil
	}
	out := make([]providers.MessageAttachment, 0, len(list))
	for _, a := range list {
		out = append(out, providers.MessageAttachment{
			Name:     a.Name,
			Path:     a.Path,
			MIMEType: a.MIMEType,
			Kind:     a.Kind,
			Caption:  a.Caption,
		})
	}
	return out
}

func contentDispositionFilename(name string) string {
	name = strings.NewReplacer("\r", "", "\n", "").Replace(name)
	name = strings.ReplaceAll(name, `\`, `\\`)
	name = strings.ReplaceAll(name, `"`, `\"`)
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return '_'
		}
		return r
	}, name)
}

var deniedViewNames = map[string]bool{
	"keyring.key":         true,
	"keyring.enc":         true,
	"config.json":         true,
	"native_clients.json": true,
}

func isDeniedViewPath(absPath string) bool {
	base := filepath.Base(absPath)
	if deniedViewNames[base] {
		return true
	}
	if strings.HasPrefix(base, "config.") {
		return true
	}
	if strings.HasSuffix(base, ".db") || strings.HasSuffix(base, ".db-wal") || strings.HasSuffix(base, ".db-shm") {
		return true
	}
	if strings.HasSuffix(base, ".key") || strings.HasSuffix(base, ".pem") {
		return true
	}
	return false
}

func (n *NativeChannel) serveFileView(w http.ResponseWriter, r *http.Request, absPath, realPath string) {
	info, err := os.Stat(realPath)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "file not found", "file_not_found")
		} else {
			writeError(w, http.StatusInternalServerError, "error accessing file", "file_error")
		}
		return
	}
	if info.IsDir() {
		writeError(w, http.StatusBadRequest, "path is a directory", "not_a_file")
		return
	}

	mimeType := detectMimeType(realPath)

	w.Header().Set("Content-Type", mimeType)
	download := r.URL.Query().Get("download") == "1"
	filename := filepath.Base(absPath)
	if override := r.URL.Query().Get("name"); override != "" {
		if base := filepath.Base(override); base != "." && base != "/" && base != string(filepath.Separator) {
			if sanitized := utils.SanitizeFilename(base); sanitized != "" && sanitized != "." {
				filename = sanitized
			}
		}
	}
	if download {
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, contentDispositionFilename(filename)))
	} else {
		w.Header().Set("Content-Disposition", "inline")
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeFile(w, r, realPath)
}

func (n *NativeChannel) handleFileView(w http.ResponseWriter, r *http.Request) {
	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		writeError(w, http.StatusBadRequest, "missing path parameter", "path_missing")
		return
	}

	absPath, err := filepath.Abs(filePath)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid path", "path_invalid")
		return
	}

	leleRoot := canonicalContainmentRoot(n.cfg.LeleDir)
	if leleRoot == "" || !isUnderAnyPublicStagingDir(absPath, leleRoot) {
		if leleRoot != "" && isContainedIn(absPath, leleRoot) {
			log.Printf("WARNING: public files/view denied non-staging path under leleDir: %s", absPath)
		}
		writeError(w, http.StatusForbidden, "access denied", "access_denied")
		return
	}

	realPath := absPath
	if resolved, err := filepath.EvalSymlinks(absPath); err == nil {
		realPath = resolved
	}
	if !isUnderAnyPublicStagingDir(realPath, leleRoot) {
		writeError(w, http.StatusForbidden, "access denied", "access_denied")
		return
	}

	if isDeniedViewPath(absPath) || isDeniedViewPath(realPath) {
		writeError(w, http.StatusForbidden, "access denied", "access_denied")
		return
	}

	n.serveFileView(w, r, absPath, realPath)
}

func (n *NativeChannel) handleFileViewSecure(w http.ResponseWriter, r *http.Request) {
	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		writeError(w, http.StatusBadRequest, "missing path parameter", "path_missing")
		return
	}

	absPath, err := filepath.Abs(filePath)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid path", "path_invalid")
		return
	}

	leleRoot := canonicalContainmentRoot(n.cfg.LeleDir)
	if leleRoot == "" || !isContainedIn(absPath, leleRoot) {
		writeError(w, http.StatusForbidden, "access denied", "access_denied")
		return
	}

	realPath := absPath
	if resolved, err := filepath.EvalSymlinks(absPath); err == nil {
		realPath = resolved
	}
	if !isContainedIn(realPath, leleRoot) {
		writeError(w, http.StatusForbidden, "access denied", "access_denied")
		return
	}

	if isDeniedViewPath(absPath) || isDeniedViewPath(realPath) {
		writeError(w, http.StatusForbidden, "access denied", "access_denied")
		return
	}

	n.serveFileView(w, r, absPath, realPath)
}
