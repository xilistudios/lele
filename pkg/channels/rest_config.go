package channels

import (
	"encoding/json"
	"net/http"

	"github.com/xilistudios/lele/pkg/config"
)

func (n *NativeChannel) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	configPath := n.getConfigPath()

	doc, meta, err := config.LoadEditableDocument(configPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load config: "+err.Error(), "config_load_failed")
		return
	}

	writeJSON(w, http.StatusOK, ConfigResponse{
		Config: doc,
		Metadata: ConfigMetadata{
			ConfigPath:              meta.ConfigPath,
			Source:                  meta.Source,
			CanSave:                 meta.CanSave,
			RestartRequiredSections: meta.RestartRequiredSections,
			SecretsByPath:           meta.SecretsByPath,
		},
	})
}

func (n *NativeChannel) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	configPath := n.getConfigPath()

	var req ConfigUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error(), "body_invalid")
		return
	}

	body, err := json.Marshal(req.Config)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid config payload", "body_invalid")
		return
	}

	var doc config.EditableDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		writeError(w, http.StatusBadRequest, "invalid config payload: "+err.Error(), "body_invalid")
		return
	}

	validationErrors := config.ValidateEditableDocument(&doc)
	if len(validationErrors) > 0 {
		httpErrors := make([]ConfigError, len(validationErrors))
		for i, err := range validationErrors {
			httpErrors[i] = ConfigError{
				Path:    err.Path,
				Message: err.Message,
				Code:    err.Code,
			}
		}
		writeJSON(w, http.StatusUnprocessableEntity, ConfigUpdateResponse{
			Errors: httpErrors,
		})
		return
	}

	if _, err := doc.ToConfig(); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "config validation failed: "+err.Error(), "config_invalid")
		return
	}

	if err := config.SaveEditableDocument(configPath, &doc); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save config: "+err.Error(), "config_save_failed")
		return
	}

	_, meta, err := config.LoadEditableDocument(configPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to reload config: "+err.Error(), "config_reload_failed")
		return
	}

	writeJSON(w, http.StatusOK, ConfigUpdateResponse{
		Config: &doc,
		Metadata: ConfigMetadata{
			ConfigPath:              meta.ConfigPath,
			Source:                  meta.Source,
			CanSave:                 meta.CanSave,
			RestartRequiredSections: meta.RestartRequiredSections,
			SecretsByPath:           meta.SecretsByPath,
		},
	})
}

func (n *NativeChannel) handleValidateConfig(w http.ResponseWriter, r *http.Request) {
	var req ConfigValidateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error(), "body_invalid")
		return
	}

	body, err := json.Marshal(req.Config)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid config payload", "body_invalid")
		return
	}

	var doc config.EditableDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		writeError(w, http.StatusBadRequest, "invalid config payload: "+err.Error(), "body_invalid")
		return
	}

	validationErrors := config.ValidateEditableDocument(&doc)
	if _, err := doc.ToConfig(); err != nil {
		validationErrors = append(validationErrors, config.ValidationError{
			Path:    "config",
			Message: err.Error(),
			Code:    "config_invalid",
		})
	}

	if len(validationErrors) > 0 {
		httpErrors := make([]ConfigError, len(validationErrors))
		for i, err := range validationErrors {
			httpErrors[i] = ConfigError{
				Path:    err.Path,
				Message: err.Message,
				Code:    err.Code,
			}
		}
		writeJSON(w, http.StatusOK, ConfigValidateResponse{
			Valid:  false,
			Errors: httpErrors,
		})
		return
	}

	writeJSON(w, http.StatusOK, ConfigValidateResponse{
		Valid:  true,
		Errors: nil,
	})
}
