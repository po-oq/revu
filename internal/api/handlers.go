package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"strconv"

	"github.com/po-oq/revu/internal/diff"
	"github.com/po-oq/revu/internal/store"
	"github.com/po-oq/revu/internal/uploads"
)

type Handler struct {
	store   *store.Store
	uploads storageBackend
}

const multipartOverheadBytes int64 = 1 << 20

type storageBackend interface {
	MaxFileBytes() int64
	Save(name, mimeType string, r io.Reader, size int64) (uploads.SavedFile, error)
	Open(relativePath string) (io.ReadCloser, error)
	Delete(relativePath string) error
}

func NewHandler(s *store.Store, storage *uploads.Storage) http.Handler {
	return newHandlerWithStorage(s, storage)
}

func newHandlerWithStorage(s *store.Store, storage storageBackend) http.Handler {
	h := &Handler{store: s, uploads: storage}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", h.health)
	mux.HandleFunc("GET /api/me", h.me)
	mux.HandleFunc("GET /api/threads", h.listThreads)
	mux.HandleFunc("POST /api/threads", h.createThread)
	mux.HandleFunc("GET /api/threads/{id}", h.getThread)
	mux.HandleFunc("PUT /api/threads/{id}", h.updateThread)
	mux.HandleFunc("DELETE /api/threads/{id}", h.deleteThread)
	mux.HandleFunc("GET /api/threads/{id}/edits", h.listThreadEdits)
	mux.HandleFunc("GET /api/threads/{id}/edits/{seq}/diff", h.threadEditDiff)
	mux.HandleFunc("POST /api/threads/{id}/comments", h.createComment)
	mux.HandleFunc("DELETE /api/comments/{id}", h.deleteComment)
	mux.HandleFunc("POST /api/uploads", h.upload)
	mux.HandleFunc("GET /api/attachments/{id}/download", h.download)
	return mux
}

func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// isLoopbackRequest treats requests from 127.0.0.1/::1 as the host device.
// RemoteAddr comes from the TCP connection, so it cannot be spoofed by
// headers; proxy headers like X-Forwarded-For are intentionally ignored.
// Parse failures count as non-host.
func isLoopbackRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"isHost": isLoopbackRequest(r)})
}

func (h *Handler) listThreads(w http.ResponseWriter, r *http.Request) {
	threads, err := h.store.ListThreads(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, threads)
}

func (h *Handler) createThread(w http.ResponseWriter, r *http.Request) {
	var req threadRequest
	if !decodeRequest(w, r, &req) {
		return
	}
	thread, err := h.store.CreateThread(r.Context(), store.CreateThreadInput{
		Type:          req.Type,
		Title:         req.Title,
		Body:          req.Body,
		OwnerDeviceID: req.DeviceID,
		AuthorName:    req.AuthorName,
		AttachmentIDs: req.AttachmentIDs,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, thread)
}

func (h *Handler) getThread(w http.ResponseWriter, r *http.Request) {
	thread, err := h.store.GetThread(r.Context(), r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, thread)
}

func (h *Handler) updateThread(w http.ResponseWriter, r *http.Request) {
	var req threadRequest
	if !decodeRequest(w, r, &req) {
		return
	}
	thread, err := h.store.UpdateThread(r.Context(), r.PathValue("id"), req.DeviceID, store.UpdateThreadInput{
		Title:      req.Title,
		Body:       req.Body,
		AuthorName: req.AuthorName,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, thread)
}

func (h *Handler) deleteThread(w http.ResponseWriter, r *http.Request) {
	var req identityRequest
	if !decodeRequest(w, r, &req) {
		return
	}
	paths, err := h.store.DeleteThreadWithAttachmentPaths(r.Context(), r.PathValue("id"), req.DeviceID, isLoopbackRequest(r))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if err := h.deleteFiles(paths); err != nil {
		writeError(w, http.StatusInternalServerError, "cleanup failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Handler) listThreadEdits(w http.ResponseWriter, r *http.Request) {
	versions, err := h.store.ListThreadVersions(r.Context(), r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": versions})
}

type diffResponse struct {
	TitleChanged bool        `json:"titleChanged"`
	OldTitle     string      `json:"oldTitle"`
	NewTitle     string      `json:"newTitle"`
	TooLarge     bool        `json:"tooLarge"`
	Hunks        []diff.Hunk `json:"hunks"`
	OldBody      string      `json:"oldBody,omitempty"`
	NewBody      string      `json:"newBody,omitempty"`
}

// threadEditDiff returns the unified diff between generation seq and seq-1.
// seq=1 diffs against an empty document, so the initial version renders as
// pure additions. Oversized diffs return tooLarge with both full bodies.
func (h *Handler) threadEditDiff(w http.ResponseWriter, r *http.Request) {
	seq, err := strconv.Atoi(r.PathValue("seq"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	older, newer, err := h.store.ThreadVersionContents(r.Context(), r.PathValue("id"), seq)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	result := diff.Lines(older.Body, newer.Body)
	resp := diffResponse{
		TitleChanged: seq > 1 && older.Title != newer.Title,
		OldTitle:     older.Title,
		NewTitle:     newer.Title,
		TooLarge:     result.TooLarge,
		Hunks:        result.Hunks,
	}
	if resp.Hunks == nil {
		resp.Hunks = []diff.Hunk{}
	}
	if result.TooLarge {
		resp.OldBody = older.Body
		resp.NewBody = newer.Body
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) createComment(w http.ResponseWriter, r *http.Request) {
	var req commentRequest
	if !decodeRequest(w, r, &req) {
		return
	}
	comment, err := h.store.CreateComment(r.Context(), r.PathValue("id"), store.CreateCommentInput{
		Body:          req.Body,
		OwnerDeviceID: req.DeviceID,
		AuthorName:    req.AuthorName,
		AttachmentIDs: req.AttachmentIDs,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, comment)
}

func (h *Handler) deleteComment(w http.ResponseWriter, r *http.Request) {
	var req identityRequest
	if !decodeRequest(w, r, &req) {
		return
	}
	paths, err := h.store.DeleteCommentWithAttachmentPaths(r.Context(), r.PathValue("id"), req.DeviceID, isLoopbackRequest(r))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if err := h.deleteFiles(paths); err != nil {
		writeError(w, http.StatusInternalServerError, "cleanup failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Handler) upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, h.uploads.MaxFileBytes()+multipartOverheadBytes)
	file, fileHeader, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid upload")
		return
	}
	defer file.Close()

	mimeType := fileHeader.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	saved, err := h.uploads.Save(fileHeader.Filename, mimeType, file, fileHeader.Size)
	if err != nil {
		if errors.Is(err, uploads.ErrTooLarge) {
			writeError(w, http.StatusBadRequest, "upload too large")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	attachment, err := h.store.CreateAttachment(r.Context(), store.CreateAttachmentInput{
		Name:        saved.OriginalName,
		Size:        saved.Size,
		MimeType:    saved.MimeType,
		StoragePath: saved.RelativePath,
	})
	if err != nil {
		_ = h.uploads.Delete(saved.RelativePath)
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, attachment)
}

func (h *Handler) download(w http.ResponseWriter, r *http.Request) {
	attachment, err := h.store.AttachmentByID(r.Context(), r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	file, err := h.uploads.Open(attachment.StoragePath)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	defer file.Close()

	w.Header().Set("Content-Type", attachment.MimeType)
	w.Header().Set("Content-Disposition", contentDisposition(attachment.Name))
	w.Header().Set("Content-Length", strconv.FormatInt(attachment.Size, 10))
	if _, err := io.Copy(w, file); err != nil {
		log.Printf("download attachment %s: %v", attachment.ID, err)
	}
}

func (h *Handler) deleteFiles(paths []string) error {
	for _, path := range paths {
		if err := h.uploads.Delete(path); err != nil {
			log.Printf("delete upload %q: %v", path, err)
			return err
		}
	}
	return nil
}

type threadRequest struct {
	Type          store.ThreadType `json:"type"`
	Title         string           `json:"title"`
	Body          string           `json:"body"`
	DeviceID      string           `json:"deviceId"`
	AuthorName    string           `json:"authorName"`
	AttachmentIDs []string         `json:"attachmentIds"`
}

type commentRequest struct {
	Body          string   `json:"body"`
	DeviceID      string   `json:"deviceId"`
	AuthorName    string   `json:"authorName"`
	AttachmentIDs []string `json:"attachmentIds"`
}

type identityRequest struct {
	DeviceID   string `json:"deviceId"`
	AuthorName string `json:"authorName"`
}

func decodeRequest(w http.ResponseWriter, r *http.Request, v any) bool {
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid input")
		return false
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid input")
		return false
	}
	return true
}

func writeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, store.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, store.ErrInvalid):
		writeError(w, http.StatusBadRequest, "invalid input")
	case errors.Is(err, uploads.ErrTooLarge):
		writeError(w, http.StatusBadRequest, "upload too large")
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func contentDisposition(name string) string {
	base := filepath.Base(name)
	if base == "." || base == string(filepath.Separator) || base == "" {
		base = "download"
	}
	return fmt.Sprintf(`attachment; filename="%s"`, escapeQuotedString(base))
}

func escapeQuotedString(value string) string {
	var out []rune
	for _, r := range value {
		if r == '"' || r == '\\' || r < 0x20 || r == 0x7f {
			out = append(out, '_')
			continue
		}
		out = append(out, r)
	}
	return string(out)
}
