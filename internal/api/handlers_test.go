package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/po-oq/revu/internal/store"
	"github.com/po-oq/revu/internal/uploads"
)

func TestCreateThreadAndGetDetail(t *testing.T) {
	handler, _, _ := newTestHandler(t)

	createReq := map[string]any{
		"type":       "markdown",
		"title":      "API thread",
		"body":       "# Hello",
		"deviceId":   "dev_owner",
		"authorName": "owner",
	}
	createRec := serveJSON(t, handler, http.MethodPost, "/api/threads", createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/threads status = %d, body %s", createRec.Code, createRec.Body.String())
	}
	var created store.Thread
	decodeJSON(t, createRec, &created)
	if created.ID == "" || created.Title != "API thread" || created.OwnerDeviceID != "dev_owner" {
		t.Fatalf("created thread mismatch: %+v", created)
	}

	getRec := serveJSON(t, handler, http.MethodGet, "/api/threads/"+created.ID, nil)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET /api/threads/{id} status = %d, body %s", getRec.Code, getRec.Body.String())
	}
	var got store.Thread
	decodeJSON(t, getRec, &got)
	if got.ID != created.ID || got.Body != "# Hello" || got.CommentCount != 0 {
		t.Fatalf("got thread mismatch: %+v", got)
	}
}

func TestWrongDeviceCannotEditThread(t *testing.T) {
	handler, _, _ := newTestHandler(t)
	created := createThread(t, handler, map[string]any{
		"type":       "text",
		"title":      "Owned thread",
		"body":       "body",
		"deviceId":   "dev_owner",
		"authorName": "owner",
	})

	updateRec := serveJSON(t, handler, http.MethodPut, "/api/threads/"+created.ID, map[string]any{
		"title":      "hijacked",
		"body":       "bad",
		"deviceId":   "dev_other",
		"authorName": "other",
	})
	if updateRec.Code != http.StatusForbidden {
		t.Fatalf("PUT wrong device status = %d, body %s", updateRec.Code, updateRec.Body.String())
	}
	assertJSONError(t, updateRec)

	getRec := serveJSON(t, handler, http.MethodGet, "/api/threads/"+created.ID, nil)
	var got store.Thread
	decodeJSON(t, getRec, &got)
	if got.Title != "Owned thread" || got.Body != "body" {
		t.Fatalf("thread changed after forbidden edit: %+v", got)
	}
}

func TestUploadAndDownload(t *testing.T) {
	handler, _, _ := newTestHandler(t)

	uploadRec := serveMultipartUpload(t, handler, "sample.txt", "text/plain", "hello upload")
	if uploadRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/uploads status = %d, body %s", uploadRec.Code, uploadRec.Body.String())
	}
	var attachment store.Attachment
	decodeJSON(t, uploadRec, &attachment)
	if attachment.ID == "" || attachment.Name != "sample.txt" || attachment.Size != int64(len("hello upload")) {
		t.Fatalf("attachment mismatch: %+v", attachment)
	}

	downloadReq := httptest.NewRequest(http.MethodGet, "/api/attachments/"+attachment.ID+"/download", nil)
	downloadRec := httptest.NewRecorder()
	handler.ServeHTTP(downloadRec, downloadReq)
	if downloadRec.Code != http.StatusOK {
		t.Fatalf("download status = %d, body %s", downloadRec.Code, downloadRec.Body.String())
	}
	if downloadRec.Body.String() != "hello upload" {
		t.Fatalf("download body = %q, want uploaded bytes", downloadRec.Body.String())
	}
	if got := downloadRec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
		t.Fatalf("Content-Type = %q, want text/plain", got)
	}
	if got := downloadRec.Header().Get("Content-Disposition"); !strings.Contains(got, `filename="sample.txt"`) {
		t.Fatalf("Content-Disposition = %q, want safe filename", got)
	}
}

func TestUploadRejectsMultipartBodyOverHTTPCap(t *testing.T) {
	handler, _, dataDir := newTestHandlerWithMaxBytes(t, 4)

	uploadRec := serveMultipartUpload(t, handler, "huge.txt", "text/plain", strings.Repeat("x", int(multipartOverheadBytes)+64))
	if uploadRec.Code != http.StatusBadRequest {
		t.Fatalf("oversized multipart status = %d, body %s", uploadRec.Code, uploadRec.Body.String())
	}
	assertJSONError(t, uploadRec)

	entries, err := os.ReadDir(filepath.Join(dataDir, "uploads"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("ReadDir uploads error: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("uploads entries after capped request = %d, want none", len(entries))
	}
}

func TestDeleteThreadAndCommentHideRowsAndRemoveFiles(t *testing.T) {
	handler, _, dataDir := newTestHandler(t)

	commentAttachment := uploadAttachment(t, handler, "comment.txt", "comment file")
	thread := createThread(t, handler, map[string]any{
		"type":       "text",
		"title":      "Comment cleanup",
		"body":       "body",
		"deviceId":   "dev_owner",
		"authorName": "owner",
	})
	commentRec := serveJSON(t, handler, http.MethodPost, "/api/threads/"+thread.ID+"/comments", map[string]any{
		"body":          "comment",
		"deviceId":      "dev_commenter",
		"authorName":    "commenter",
		"attachmentIds": []string{commentAttachment.ID},
	})
	if commentRec.Code != http.StatusCreated {
		t.Fatalf("POST comment status = %d, body %s", commentRec.Code, commentRec.Body.String())
	}
	var comment store.Comment
	decodeJSON(t, commentRec, &comment)
	commentPath := soleUploadPath(t, dataDir)

	deleteCommentRec := serveJSON(t, handler, http.MethodDelete, "/api/comments/"+comment.ID, map[string]any{
		"deviceId":   "dev_commenter",
		"authorName": "commenter",
	})
	if deleteCommentRec.Code != http.StatusOK {
		t.Fatalf("DELETE comment status = %d, body %s", deleteCommentRec.Code, deleteCommentRec.Body.String())
	}
	assertOK(t, deleteCommentRec)
	if _, err := os.Stat(commentPath); !os.IsNotExist(err) {
		t.Fatalf("comment attachment stat error = %v, want not exist", err)
	}
	var afterCommentDelete store.Thread
	decodeJSON(t, serveJSON(t, handler, http.MethodGet, "/api/threads/"+thread.ID, nil), &afterCommentDelete)
	if len(afterCommentDelete.Comments) != 0 {
		t.Fatalf("comments after delete = %+v, want none", afterCommentDelete.Comments)
	}

	threadAttachment := uploadAttachment(t, handler, "thread.txt", "thread file")
	threadWithAttachment := createThread(t, handler, map[string]any{
		"type":          "text",
		"title":         "Thread cleanup",
		"body":          "body",
		"deviceId":      "dev_owner",
		"authorName":    "owner",
		"attachmentIds": []string{threadAttachment.ID},
	})
	threadPath := soleUploadPath(t, dataDir)

	deleteThreadRec := serveJSON(t, handler, http.MethodDelete, "/api/threads/"+threadWithAttachment.ID, map[string]any{
		"deviceId":   "dev_owner",
		"authorName": "owner",
	})
	if deleteThreadRec.Code != http.StatusOK {
		t.Fatalf("DELETE thread status = %d, body %s", deleteThreadRec.Code, deleteThreadRec.Body.String())
	}
	assertOK(t, deleteThreadRec)
	if _, err := os.Stat(threadPath); !os.IsNotExist(err) {
		t.Fatalf("thread attachment stat error = %v, want not exist", err)
	}
	getDeletedRec := serveJSON(t, handler, http.MethodGet, "/api/threads/"+threadWithAttachment.ID, nil)
	if getDeletedRec.Code != http.StatusNotFound {
		t.Fatalf("GET deleted thread status = %d, body %s", getDeletedRec.Code, getDeletedRec.Body.String())
	}
}

func TestDeleteThreadReportsCleanupFailure(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	s, err := store.Open(ctx, filepath.Join(dataDir, "revu.sqlite"))
	if err != nil {
		t.Fatalf("store.Open error: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	storage := &failingDeleteStorage{Storage: uploads.NewStorage(filepath.Join(dataDir, "uploads"), 1024)}
	handler := newHandlerWithStorage(s, storage)

	attachment := uploadAttachment(t, handler, "thread.txt", "thread file")
	thread := createThread(t, handler, map[string]any{
		"type":          "text",
		"title":         "Thread cleanup",
		"body":          "body",
		"deviceId":      "dev_owner",
		"authorName":    "owner",
		"attachmentIds": []string{attachment.ID},
	})

	deleteRec := serveJSON(t, handler, http.MethodDelete, "/api/threads/"+thread.ID, map[string]any{
		"deviceId":   "dev_owner",
		"authorName": "owner",
	})
	if deleteRec.Code != http.StatusInternalServerError {
		t.Fatalf("DELETE cleanup failure status = %d, body %s", deleteRec.Code, deleteRec.Body.String())
	}
	assertJSONError(t, deleteRec)
	getDeletedRec := serveJSON(t, handler, http.MethodGet, "/api/threads/"+thread.ID, nil)
	if getDeletedRec.Code != http.StatusNotFound {
		t.Fatalf("GET after cleanup failure status = %d, body %s", getDeletedRec.Code, getDeletedRec.Body.String())
	}
}

func newTestHandler(t *testing.T) (http.Handler, *store.Store, string) {
	return newTestHandlerWithMaxBytes(t, 1024)
}

func newTestHandlerWithMaxBytes(t *testing.T, maxBytes int64) (http.Handler, *store.Store, string) {
	t.Helper()
	ctx := context.Background()
	dataDir := t.TempDir()
	s, err := store.Open(ctx, filepath.Join(dataDir, "revu.sqlite"))
	if err != nil {
		t.Fatalf("store.Open error: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	storage := uploads.NewStorage(filepath.Join(dataDir, "uploads"), maxBytes)
	return NewHandler(s, storage), s, dataDir
}

// httptest.NewRequest defaults RemoteAddr to 192.0.2.1:1234, so plain
// serveJSON requests are always treated as non-host.
func serveJSON(t *testing.T, handler http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return serveJSONFrom(t, handler, "192.0.2.1:1234", method, path, body)
}

func serveJSONFrom(t *testing.T, handler http.Handler, remoteAddr, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != nil {
		var buf bytes.Buffer
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("Encode request body error: %v", err)
		}
		r = &buf
	}
	req := httptest.NewRequest(method, path, r)
	req.RemoteAddr = remoteAddr
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func serveMultipartUpload(t *testing.T, handler http.Handler, filename, contentType, body string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="file"; filename="`+filename+`"`)
	header.Set("Content-Type", contentType)
	header.Set("Content-Length", strconv.Itoa(len(body)))
	part, err := w.CreatePart(header)
	if err != nil {
		t.Fatalf("CreateFormFile error: %v", err)
	}
	if _, err := part.Write([]byte(body)); err != nil {
		t.Fatalf("write upload body error: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("multipart close error: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/uploads", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.NewDecoder(rec.Body).Decode(v); err != nil {
		t.Fatalf("Decode response %q error: %v", rec.Body.String(), err)
	}
}

func assertJSONError(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	var got map[string]string
	decodeJSON(t, rec, &got)
	if got["error"] == "" {
		t.Fatalf("error response = %+v, want non-empty error", got)
	}
}

func assertOK(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	var got map[string]bool
	decodeJSON(t, rec, &got)
	if !got["ok"] {
		t.Fatalf("ok response = %+v, want ok true", got)
	}
}

func createThread(t *testing.T, handler http.Handler, body map[string]any) store.Thread {
	t.Helper()
	rec := serveJSON(t, handler, http.MethodPost, "/api/threads", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("createThread status = %d, body %s", rec.Code, rec.Body.String())
	}
	var thread store.Thread
	decodeJSON(t, rec, &thread)
	return thread
}

func uploadAttachment(t *testing.T, handler http.Handler, filename, body string) store.Attachment {
	t.Helper()
	rec := serveMultipartUpload(t, handler, filename, "text/plain", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("uploadAttachment status = %d, body %s", rec.Code, rec.Body.String())
	}
	var attachment store.Attachment
	decodeJSON(t, rec, &attachment)
	return attachment
}

func soleUploadPath(t *testing.T, dataDir string) string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dataDir, "uploads"))
	if err != nil {
		t.Fatalf("ReadDir uploads error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("uploads entries = %d, want one", len(entries))
	}
	return filepath.Join(dataDir, "uploads", entries[0].Name())
}

type failingDeleteStorage struct {
	*uploads.Storage
}

func (s *failingDeleteStorage) Delete(string) error {
	return errDeleteFailed
}

var errDeleteFailed = os.ErrPermission

func TestMeReportsHostOnlyForLoopback(t *testing.T) {
	handler, _, _ := newTestHandler(t)
	for _, tc := range []struct {
		remoteAddr string
		wantHost   bool
	}{
		{"127.0.0.1:52000", true},
		{"[::1]:52000", true},
		{"192.168.1.20:52000", false},
		{"unparseable", false},
	} {
		rec := serveJSONFrom(t, handler, tc.remoteAddr, http.MethodGet, "/api/me", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/me from %s status = %d, body %s", tc.remoteAddr, rec.Code, rec.Body.String())
		}
		var got map[string]bool
		decodeJSON(t, rec, &got)
		if got["isHost"] != tc.wantHost {
			t.Fatalf("isHost from %s = %v, want %v", tc.remoteAddr, got["isHost"], tc.wantHost)
		}
	}
}

func TestLoopbackHostCanDeleteOthersThreadAndComment(t *testing.T) {
	handler, _, _ := newTestHandler(t)
	thread := createThread(t, handler, map[string]any{
		"type":       "text",
		"title":      "host delete target",
		"body":       "body",
		"deviceId":   "dev_owner",
		"authorName": "owner",
	})
	commentRec := serveJSON(t, handler, http.MethodPost, "/api/threads/"+thread.ID+"/comments", map[string]any{
		"body":       "target comment",
		"deviceId":   "dev_commenter",
		"authorName": "commenter",
	})
	if commentRec.Code != http.StatusCreated {
		t.Fatalf("create comment status = %d, body %s", commentRec.Code, commentRec.Body.String())
	}
	var comment store.Comment
	decodeJSON(t, commentRec, &comment)

	delCommentRec := serveJSONFrom(t, handler, "127.0.0.1:50001", http.MethodDelete, "/api/comments/"+comment.ID, map[string]any{
		"deviceId":   "dev_host",
		"authorName": "host",
	})
	if delCommentRec.Code != http.StatusOK {
		t.Fatalf("host delete comment status = %d, body %s", delCommentRec.Code, delCommentRec.Body.String())
	}
	assertOK(t, delCommentRec)

	delThreadRec := serveJSONFrom(t, handler, "127.0.0.1:50002", http.MethodDelete, "/api/threads/"+thread.ID, map[string]any{
		"deviceId":   "dev_host",
		"authorName": "host",
	})
	if delThreadRec.Code != http.StatusOK {
		t.Fatalf("host delete thread status = %d, body %s", delThreadRec.Code, delThreadRec.Body.String())
	}
	assertOK(t, delThreadRec)

	getRec := serveJSON(t, handler, http.MethodGet, "/api/threads/"+thread.ID, nil)
	if getRec.Code != http.StatusNotFound {
		t.Fatalf("thread after host delete status = %d, want 404", getRec.Code)
	}
}

func TestNonLoopbackDeviceStillCannotDeleteOthersThread(t *testing.T) {
	handler, _, _ := newTestHandler(t)
	thread := createThread(t, handler, map[string]any{
		"type":       "text",
		"title":      "protected thread",
		"body":       "body",
		"deviceId":   "dev_owner",
		"authorName": "owner",
	})
	rec := serveJSONFrom(t, handler, "192.168.1.20:50001", http.MethodDelete, "/api/threads/"+thread.ID, map[string]any{
		"deviceId":   "dev_other",
		"authorName": "other",
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-loopback delete status = %d, want 403, body %s", rec.Code, rec.Body.String())
	}
	assertJSONError(t, rec)
}

type diffResponseBody struct {
	TitleChanged bool   `json:"titleChanged"`
	OldTitle     string `json:"oldTitle"`
	NewTitle     string `json:"newTitle"`
	TooLarge     bool   `json:"tooLarge"`
	Hunks        []struct {
		OldStart int `json:"oldStart"`
		NewStart int `json:"newStart"`
		Lines    []struct {
			Op   string `json:"op"`
			Text string `json:"text"`
		} `json:"lines"`
	} `json:"hunks"`
	OldBody string `json:"oldBody"`
	NewBody string `json:"newBody"`
}

func TestThreadEditHistoryAndDiff(t *testing.T) {
	handler, _, _ := newTestHandler(t)
	thread := createThread(t, handler, map[string]any{
		"type": "text", "title": "v1 title", "body": "alpha\nbeta",
		"deviceId": "dev_owner", "authorName": "owner",
	})
	putRec := serveJSON(t, handler, http.MethodPut, "/api/threads/"+thread.ID, map[string]any{
		"title": "v2 title", "body": "alpha\ngamma",
		"deviceId": "dev_owner", "authorName": "owner",
	})
	if putRec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, body %s", putRec.Code, putRec.Body.String())
	}

	editsRec := serveJSON(t, handler, http.MethodGet, "/api/threads/"+thread.ID+"/edits", nil)
	if editsRec.Code != http.StatusOK {
		t.Fatalf("GET edits status = %d, body %s", editsRec.Code, editsRec.Body.String())
	}
	var edits struct {
		Versions []store.ThreadVersion `json:"versions"`
	}
	decodeJSON(t, editsRec, &edits)
	if len(edits.Versions) != 2 {
		t.Fatalf("versions = %+v, want 2", edits.Versions)
	}
	if edits.Versions[0].Seq != 2 || !edits.Versions[0].IsCurrent || edits.Versions[0].Title != "v2 title" {
		t.Fatalf("current version mismatch: %+v", edits.Versions[0])
	}
	if edits.Versions[1].Seq != 1 || edits.Versions[1].Title != "v1 title" {
		t.Fatalf("initial version mismatch: %+v", edits.Versions[1])
	}

	diffRec := serveJSON(t, handler, http.MethodGet, "/api/threads/"+thread.ID+"/edits/2/diff", nil)
	if diffRec.Code != http.StatusOK {
		t.Fatalf("GET diff status = %d, body %s", diffRec.Code, diffRec.Body.String())
	}
	var d diffResponseBody
	decodeJSON(t, diffRec, &d)
	if !d.TitleChanged || d.OldTitle != "v1 title" || d.NewTitle != "v2 title" || d.TooLarge {
		t.Fatalf("diff meta mismatch: %+v", d)
	}
	if len(d.Hunks) != 1 || len(d.Hunks[0].Lines) != 3 {
		t.Fatalf("diff hunks mismatch: %+v", d.Hunks)
	}
	ops := []string{d.Hunks[0].Lines[0].Op, d.Hunks[0].Lines[1].Op, d.Hunks[0].Lines[2].Op}
	if ops[0] != "ctx" || ops[1] != "del" || ops[2] != "add" {
		t.Fatalf("diff ops = %v, want [ctx del add]", ops)
	}

	initialRec := serveJSON(t, handler, http.MethodGet, "/api/threads/"+thread.ID+"/edits/1/diff", nil)
	if initialRec.Code != http.StatusOK {
		t.Fatalf("GET initial diff status = %d, body %s", initialRec.Code, initialRec.Body.String())
	}
	var initial diffResponseBody
	decodeJSON(t, initialRec, &initial)
	if initial.TitleChanged {
		t.Fatalf("initial version should not report a title change: %+v", initial)
	}
	if len(initial.Hunks) != 1 || len(initial.Hunks[0].Lines) != 2 ||
		initial.Hunks[0].Lines[0].Op != "add" || initial.Hunks[0].Lines[1].Op != "add" {
		t.Fatalf("initial diff should be all adds: %+v", initial.Hunks)
	}

	for _, path := range []string{
		"/api/threads/" + thread.ID + "/edits/0/diff",
		"/api/threads/" + thread.ID + "/edits/3/diff",
		"/api/threads/" + thread.ID + "/edits/abc/diff",
		"/api/threads/thr_missing/edits",
		"/api/threads/thr_missing/edits/1/diff",
	} {
		rec := serveJSON(t, handler, http.MethodGet, path, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("GET %s status = %d, want 404", path, rec.Code)
		}
		assertJSONError(t, rec)
	}
}

func TestCommentAndThreadCarryVersionNumbers(t *testing.T) {
	handler, _, _ := newTestHandler(t)

	createRec := serveJSON(t, handler, http.MethodPost, "/api/threads", map[string]any{
		"type": "markdown", "title": "versioned", "body": "# v1",
		"deviceId": "dev_owner", "authorName": "owner",
	})
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create thread status = %d, body %s", createRec.Code, createRec.Body.String())
	}
	var created store.Thread
	decodeJSON(t, createRec, &created)
	if created.CurrentVersion != 1 {
		t.Fatalf("created.CurrentVersion = %d, want 1", created.CurrentVersion)
	}

	c1Rec := serveJSON(t, handler, http.MethodPost, "/api/threads/"+created.ID+"/comments", map[string]any{
		"body": "first", "deviceId": "dev_b", "authorName": "poo",
	})
	if c1Rec.Code != http.StatusCreated {
		t.Fatalf("create comment status = %d, body %s", c1Rec.Code, c1Rec.Body.String())
	}
	var c1 store.Comment
	decodeJSON(t, c1Rec, &c1)
	if c1.ThreadVersion != 1 {
		t.Fatalf("c1.ThreadVersion = %d, want 1", c1.ThreadVersion)
	}

	editRec := serveJSON(t, handler, http.MethodPut, "/api/threads/"+created.ID, map[string]any{
		"title": "versioned", "body": "# v2", "deviceId": "dev_owner", "authorName": "owner",
	})
	if editRec.Code != http.StatusOK {
		t.Fatalf("edit thread status = %d, body %s", editRec.Code, editRec.Body.String())
	}

	c2Rec := serveJSON(t, handler, http.MethodPost, "/api/threads/"+created.ID+"/comments", map[string]any{
		"body": "second", "deviceId": "dev_b", "authorName": "poo",
	})
	if c2Rec.Code != http.StatusCreated {
		t.Fatalf("create comment status = %d, body %s", c2Rec.Code, c2Rec.Body.String())
	}
	var c2 store.Comment
	decodeJSON(t, c2Rec, &c2)
	if c2.ThreadVersion != 2 {
		t.Fatalf("c2.ThreadVersion = %d, want 2", c2.ThreadVersion)
	}

	getRec := serveJSON(t, handler, http.MethodGet, "/api/threads/"+created.ID, nil)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get thread status = %d", getRec.Code)
	}
	var got store.Thread
	decodeJSON(t, getRec, &got)
	if got.CurrentVersion != 2 {
		t.Fatalf("got.CurrentVersion = %d, want 2", got.CurrentVersion)
	}
	if len(got.Comments) != 2 || got.Comments[0].ThreadVersion != 1 || got.Comments[1].ThreadVersion != 2 {
		t.Fatalf("comment versions mismatch: %+v", got.Comments)
	}
}

func TestNoOpEditDoesNotAddVersion(t *testing.T) {
	handler, _, _ := newTestHandler(t)
	thread := createThread(t, handler, map[string]any{
		"type": "text", "title": "same", "body": "same body",
		"deviceId": "dev_owner", "authorName": "owner",
	})
	putRec := serveJSON(t, handler, http.MethodPut, "/api/threads/"+thread.ID, map[string]any{
		"title": "same", "body": "same body",
		"deviceId": "dev_owner", "authorName": "owner",
	})
	if putRec.Code != http.StatusOK {
		t.Fatalf("no-op PUT status = %d, body %s", putRec.Code, putRec.Body.String())
	}
	editsRec := serveJSON(t, handler, http.MethodGet, "/api/threads/"+thread.ID+"/edits", nil)
	var edits struct {
		Versions []store.ThreadVersion `json:"versions"`
	}
	decodeJSON(t, editsRec, &edits)
	if len(edits.Versions) != 1 || !edits.Versions[0].IsCurrent {
		t.Fatalf("versions after no-op = %+v, want single current version", edits.Versions)
	}
}
