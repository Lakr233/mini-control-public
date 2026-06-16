package rest

import (
	"errors"
	"net/http"

	"github.com/Lakr233/minis/server/internal/release"
)

const maxReleaseUploadBytes = 256 << 20

func (h *Handler) handleReleaseUpload(w http.ResponseWriter, r *http.Request) {
	if h.releases == nil {
		writeError(w, http.StatusServiceUnavailable, "release management not configured")
		return
	}

	if err := r.ParseMultipartForm(maxReleaseUploadBytes); err != nil {
		writeError(w, http.StatusBadRequest, "invalid multipart form")
		return
	}

	version := r.FormValue("version")
	if version == "" {
		writeError(w, http.StatusBadRequest, "version is required")
		return
	}
	if err := release.ValidateVersion(version); err != nil {
		writeError(w, http.StatusBadRequest, "invalid version")
		return
	}

	file, _, err := r.FormFile("binary")
	if err != nil {
		writeError(w, http.StatusBadRequest, "binary file is required")
		return
	}
	defer file.Close()

	rel, err := h.releases.Upload(r.Context(), version, file)
	if err != nil {
		h.logger.Error("release upload failed", "version", version, "error", err)
		if errors.Is(err, release.ErrReleaseVersionExists) {
			writeError(w, http.StatusConflict, "release version already exists")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	h.logger.Info("release uploaded", "version", version, "sha256", rel.SHA256)
	writeJSON(w, http.StatusCreated, rel)
}

func (h *Handler) handleReleaseUploadBinary(w http.ResponseWriter, r *http.Request) {
	if h.releases == nil {
		writeError(w, http.StatusServiceUnavailable, "release management not configured")
		return
	}

	version := r.PathValue("version")
	if err := release.ValidateVersion(version); err != nil {
		writeError(w, http.StatusBadRequest, "invalid version")
		return
	}
	if r.Body == nil {
		writeError(w, http.StatusBadRequest, "binary body is required")
		return
	}
	defer r.Body.Close()

	rel, err := h.releases.Upload(r.Context(), version, r.Body)
	if err != nil {
		h.logger.Error("release raw upload failed", "version", version, "error", err)
		if errors.Is(err, release.ErrReleaseVersionExists) {
			writeError(w, http.StatusConflict, "release version already exists")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	h.logger.Info("release uploaded", "version", version, "sha256", rel.SHA256)
	writeJSON(w, http.StatusCreated, rel)
}

func (h *Handler) handleReleaseLatest(w http.ResponseWriter, r *http.Request) {
	if h.releases == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	latest, err := h.releases.GetLatest(r.Context())
	if err != nil {
		writeError(w, http.StatusNotFound, "no releases available")
		return
	}

	writeJSON(w, http.StatusOK, latest)
}

func (h *Handler) handleReleaseDownload(w http.ResponseWriter, r *http.Request) {
	if h.releases == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	version := r.PathValue("version")
	if err := release.ValidateVersion(version); err != nil {
		writeError(w, http.StatusBadRequest, "invalid version")
		return
	}
	rel, err := h.releases.GetByVersion(r.Context(), version)
	if err != nil {
		writeError(w, http.StatusNotFound, "version not found")
		return
	}

	binaryPath, err := h.releases.BinaryPath(rel)
	if err != nil {
		h.logger.Error("invalid release binary path", "version", version, "error", err)
		writeError(w, http.StatusInternalServerError, "release unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename=minicontrol-worker")
	w.Header().Set("X-Release-SHA256", rel.SHA256)
	w.Header().Set("X-Release-Version", rel.Version)
	http.ServeFile(w, r, binaryPath)
}
