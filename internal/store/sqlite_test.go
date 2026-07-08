package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "revu.sqlite")
	s, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("Open error: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestSeedInsertsSamplesOnlyOnce(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	if err := s.SeedIfEmpty(ctx); err != nil {
		t.Fatalf("first seed error: %v", err)
	}
	first, err := s.ListThreads(ctx)
	if err != nil {
		t.Fatalf("ListThreads error: %v", err)
	}
	if len(first) < 4 {
		t.Fatalf("seed thread count = %d, want at least 4", len(first))
	}
	if err := s.SeedIfEmpty(ctx); err != nil {
		t.Fatalf("second seed error: %v", err)
	}
	second, err := s.ListThreads(ctx)
	if err != nil {
		t.Fatalf("ListThreads error: %v", err)
	}
	if len(second) != len(first) {
		t.Fatalf("seed ran twice: before %d after %d", len(first), len(second))
	}
}

func TestCreateThreadListAndGet(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	thread, err := s.CreateThread(ctx, CreateThreadInput{
		Type:          TypeMarkdown,
		Title:         "Markdownレビュー",
		Body:          "# Hello",
		OwnerDeviceID: "dev_a",
		AuthorName:    "wata",
	})
	if err != nil {
		t.Fatalf("CreateThread error: %v", err)
	}
	got, err := s.GetThread(ctx, thread.ID)
	if err != nil {
		t.Fatalf("GetThread error: %v", err)
	}
	if got.Title != "Markdownレビュー" || got.OwnerDeviceID != "dev_a" {
		t.Fatalf("thread mismatch: %+v", got)
	}
	list, err := s.ListThreads(ctx)
	if err != nil {
		t.Fatalf("ListThreads error: %v", err)
	}
	if len(list) != 1 || list[0].ID != thread.ID {
		t.Fatalf("list mismatch: %+v", list)
	}
}

func TestOwnerDeviceIDControlsEditAndDelete(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	thread, err := s.CreateThread(ctx, CreateThreadInput{
		Type:          TypeText,
		Title:         "レビュー対象",
		Body:          "body",
		OwnerDeviceID: "dev_owner",
		AuthorName:    "owner",
	})
	if err != nil {
		t.Fatalf("CreateThread error: %v", err)
	}
	if _, err := s.UpdateThread(ctx, thread.ID, "dev_other", UpdateThreadInput{Title: "bad", Body: "bad"}); err != ErrForbidden {
		t.Fatalf("UpdateThread wrong device error = %v, want ErrForbidden", err)
	}
	if _, err := s.UpdateThread(ctx, thread.ID, "dev_owner", UpdateThreadInput{Title: "good", Body: "changed", AuthorName: "owner"}); err != nil {
		t.Fatalf("UpdateThread owner error: %v", err)
	}
	if err := s.DeleteThread(ctx, thread.ID, "dev_other"); err != ErrForbidden {
		t.Fatalf("DeleteThread wrong device error = %v, want ErrForbidden", err)
	}
	if err := s.DeleteThread(ctx, thread.ID, "dev_owner"); err != nil {
		t.Fatalf("DeleteThread owner error: %v", err)
	}
	if _, err := s.GetThread(ctx, thread.ID); err != ErrNotFound {
		t.Fatalf("GetThread deleted error = %v, want ErrNotFound", err)
	}
}

func TestCommentNumbersDoNotReuseAfterDelete(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	thread, err := s.CreateThread(ctx, CreateThreadInput{
		Type:          TypeText,
		Title:         "レビュー対象",
		Body:          "body",
		OwnerDeviceID: "dev_owner",
		AuthorName:    "owner",
	})
	if err != nil {
		t.Fatalf("CreateThread error: %v", err)
	}
	first, err := s.CreateComment(ctx, thread.ID, CreateCommentInput{Body: "first", OwnerDeviceID: "dev_a", AuthorName: "a"})
	if err != nil {
		t.Fatalf("CreateComment first error: %v", err)
	}
	if err := s.DeleteComment(ctx, first.ID, "dev_a"); err != nil {
		t.Fatalf("DeleteComment error: %v", err)
	}
	second, err := s.CreateComment(ctx, thread.ID, CreateCommentInput{Body: "second", OwnerDeviceID: "dev_b", AuthorName: "b"})
	if err != nil {
		t.Fatalf("CreateComment second error: %v", err)
	}
	if second.Number != 2 {
		t.Fatalf("second comment number = %d, want 2", second.Number)
	}
	comments, err := s.ListComments(ctx, thread.ID)
	if err != nil {
		t.Fatalf("ListComments error: %v", err)
	}
	if len(comments) != 1 || comments[0].Number != 2 {
		t.Fatalf("visible comments = %+v, want only #2", comments)
	}
}

func TestAttachmentCreateListAndClaim(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	attachment, err := s.CreateAttachment(ctx, CreateAttachmentInput{
		Name:        "sample.txt",
		Size:        12,
		MimeType:    "text/plain",
		StoragePath: "uploads/sample.txt",
	})
	if err != nil {
		t.Fatalf("CreateAttachment error: %v", err)
	}
	if attachment.OwnerKind != "" || attachment.OwnerID != "" {
		t.Fatalf("pending attachment owner = %q/%q, want empty", attachment.OwnerKind, attachment.OwnerID)
	}
	if attachment.DownloadURL != "/api/attachments/"+attachment.ID+"/download" {
		t.Fatalf("DownloadURL = %q", attachment.DownloadURL)
	}
	pending, err := s.ListAttachments(ctx, "", "")
	if err != nil {
		t.Fatalf("ListAttachments pending error: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != attachment.ID {
		t.Fatalf("pending attachments = %+v, want created attachment", pending)
	}
	thread, err := s.CreateThread(ctx, CreateThreadInput{
		Type:          TypeText,
		Title:         "添付つき",
		Body:          "body",
		OwnerDeviceID: "dev_owner",
		AuthorName:    "owner",
		AttachmentIDs: []string{attachment.ID},
	})
	if err != nil {
		t.Fatalf("CreateThread with attachment error: %v", err)
	}
	threadAttachments, err := s.ListAttachments(ctx, "thread", thread.ID)
	if err != nil {
		t.Fatalf("ListAttachments thread error: %v", err)
	}
	if len(threadAttachments) != 1 || threadAttachments[0].ID != attachment.ID {
		t.Fatalf("thread attachments = %+v, want claimed attachment", threadAttachments)
	}
	pending, err = s.ListAttachments(ctx, "", "")
	if err != nil {
		t.Fatalf("ListAttachments pending after claim error: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending attachments after claim = %+v, want none", pending)
	}
}

func TestAttachmentByID(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	attachment, err := s.CreateAttachment(ctx, CreateAttachmentInput{
		Name:        "sample.txt",
		Size:        12,
		MimeType:    "text/plain",
		StoragePath: "uploads/sample.txt",
	})
	if err != nil {
		t.Fatalf("CreateAttachment error: %v", err)
	}
	got, err := s.AttachmentByID(ctx, attachment.ID)
	if err != nil {
		t.Fatalf("AttachmentByID error: %v", err)
	}
	if got.ID != attachment.ID || got.StoragePath != "uploads/sample.txt" || got.DownloadURL == "" {
		t.Fatalf("attachment mismatch: %+v", got)
	}
	if _, err := s.AttachmentByID(ctx, "att_missing"); err != ErrNotFound {
		t.Fatalf("AttachmentByID missing error = %v, want ErrNotFound", err)
	}
}

func TestClaimedAttachmentCannotBeReclaimed(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	attachment, err := s.CreateAttachment(ctx, CreateAttachmentInput{
		Name:        "sample.txt",
		Size:        12,
		MimeType:    "text/plain",
		StoragePath: "uploads/sample.txt",
	})
	if err != nil {
		t.Fatalf("CreateAttachment error: %v", err)
	}
	first, err := s.CreateThread(ctx, CreateThreadInput{
		Type:          TypeText,
		Title:         "first",
		Body:          "body",
		OwnerDeviceID: "dev_a",
		AuthorName:    "a",
		AttachmentIDs: []string{attachment.ID},
	})
	if err != nil {
		t.Fatalf("CreateThread first error: %v", err)
	}
	if _, err := s.CreateThread(ctx, CreateThreadInput{
		Type:          TypeText,
		Title:         "second",
		Body:          "body",
		OwnerDeviceID: "dev_b",
		AuthorName:    "b",
		AttachmentIDs: []string{attachment.ID},
	}); err != ErrNotFound {
		t.Fatalf("CreateThread reusing attachment error = %v, want ErrNotFound", err)
	}
	firstAttachments, err := s.ListAttachments(ctx, "thread", first.ID)
	if err != nil {
		t.Fatalf("ListAttachments first error: %v", err)
	}
	if len(firstAttachments) != 1 || firstAttachments[0].ID != attachment.ID {
		t.Fatalf("first attachments = %+v, want original attachment", firstAttachments)
	}
	commentAttachment, err := s.CreateAttachment(ctx, CreateAttachmentInput{
		Name:        "comment.txt",
		Size:        12,
		MimeType:    "text/plain",
		StoragePath: "uploads/comment.txt",
	})
	if err != nil {
		t.Fatalf("CreateAttachment comment error: %v", err)
	}
	comment, err := s.CreateComment(ctx, first.ID, CreateCommentInput{
		Body:          "comment",
		OwnerDeviceID: "dev_commenter",
		AuthorName:    "commenter",
		AttachmentIDs: []string{commentAttachment.ID},
	})
	if err != nil {
		t.Fatalf("CreateComment with attachment error: %v", err)
	}
	if _, err := s.CreateComment(ctx, first.ID, CreateCommentInput{
		Body:          "reclaim",
		OwnerDeviceID: "dev_other",
		AuthorName:    "other",
		AttachmentIDs: []string{commentAttachment.ID},
	}); err != ErrNotFound {
		t.Fatalf("CreateComment reusing attachment error = %v, want ErrNotFound", err)
	}
	commentAttachments, err := s.ListAttachments(ctx, "comment", comment.ID)
	if err != nil {
		t.Fatalf("ListAttachments comment error: %v", err)
	}
	if len(commentAttachments) != 1 || commentAttachments[0].ID != commentAttachment.ID {
		t.Fatalf("comment attachments = %+v, want original attachment", commentAttachments)
	}
}

func TestInvalidAttachmentIDRollsBackThreadAndCommentCreation(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	if _, err := s.CreateThread(ctx, CreateThreadInput{
		Type:          TypeText,
		Title:         "bad attachment",
		Body:          "body",
		OwnerDeviceID: "dev_owner",
		AuthorName:    "owner",
		AttachmentIDs: []string{"att_missing"},
	}); err != ErrNotFound {
		t.Fatalf("CreateThread invalid attachment error = %v, want ErrNotFound", err)
	}
	threads, err := s.ListThreads(ctx)
	if err != nil {
		t.Fatalf("ListThreads error: %v", err)
	}
	if len(threads) != 0 {
		t.Fatalf("threads after failed create = %+v, want none", threads)
	}
	thread, err := s.CreateThread(ctx, CreateThreadInput{
		Type:          TypeText,
		Title:         "valid",
		Body:          "body",
		OwnerDeviceID: "dev_owner",
		AuthorName:    "owner",
	})
	if err != nil {
		t.Fatalf("CreateThread valid error: %v", err)
	}
	if _, err := s.CreateComment(ctx, thread.ID, CreateCommentInput{
		Body:          "bad attachment",
		OwnerDeviceID: "dev_commenter",
		AuthorName:    "commenter",
		AttachmentIDs: []string{"att_missing"},
	}); err != ErrNotFound {
		t.Fatalf("CreateComment invalid attachment error = %v, want ErrNotFound", err)
	}
	comments, err := s.ListComments(ctx, thread.ID)
	if err != nil {
		t.Fatalf("ListComments error: %v", err)
	}
	if len(comments) != 0 {
		t.Fatalf("comments after failed create = %+v, want none", comments)
	}
}

func TestDeleteThreadWithAttachmentPathsChecksOwnerAndSoftDeletesAttachments(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	threadAttachment, err := s.CreateAttachment(ctx, CreateAttachmentInput{
		Name:        "thread.txt",
		Size:        1,
		MimeType:    "text/plain",
		StoragePath: "uploads/thread.txt",
	})
	if err != nil {
		t.Fatalf("CreateAttachment thread error: %v", err)
	}
	commentAttachment, err := s.CreateAttachment(ctx, CreateAttachmentInput{
		Name:        "comment.txt",
		Size:        1,
		MimeType:    "text/plain",
		StoragePath: "uploads/comment.txt",
	})
	if err != nil {
		t.Fatalf("CreateAttachment comment error: %v", err)
	}
	thread, err := s.CreateThread(ctx, CreateThreadInput{
		Type:          TypeText,
		Title:         "with attachments",
		Body:          "body",
		OwnerDeviceID: "dev_owner",
		AuthorName:    "owner",
		AttachmentIDs: []string{threadAttachment.ID},
	})
	if err != nil {
		t.Fatalf("CreateThread error: %v", err)
	}
	comment, err := s.CreateComment(ctx, thread.ID, CreateCommentInput{
		Body:          "comment",
		OwnerDeviceID: "dev_commenter",
		AuthorName:    "commenter",
		AttachmentIDs: []string{commentAttachment.ID},
	})
	if err != nil {
		t.Fatalf("CreateComment error: %v", err)
	}
	paths, err := s.DeleteThreadWithAttachmentPaths(ctx, thread.ID, "dev_other", false)
	if err != ErrForbidden {
		t.Fatalf("DeleteThreadWithAttachmentPaths wrong device error = %v, want ErrForbidden", err)
	}
	if len(paths) != 0 {
		t.Fatalf("paths for wrong device = %+v, want none", paths)
	}
	threadAttachments, err := s.ListAttachments(ctx, "thread", thread.ID)
	if err != nil {
		t.Fatalf("ListAttachments thread before owner delete error: %v", err)
	}
	commentAttachments, err := s.ListAttachments(ctx, "comment", comment.ID)
	if err != nil {
		t.Fatalf("ListAttachments comment before owner delete error: %v", err)
	}
	if len(threadAttachments) != 1 || len(commentAttachments) != 1 {
		t.Fatalf("attachments after forbidden delete = thread:%+v comment:%+v, want both present", threadAttachments, commentAttachments)
	}
	paths, err = s.DeleteThreadWithAttachmentPaths(ctx, thread.ID, "dev_owner", false)
	if err != nil {
		t.Fatalf("DeleteThreadWithAttachmentPaths owner error: %v", err)
	}
	if !sameStrings(paths, []string{"uploads/thread.txt", "uploads/comment.txt"}) {
		t.Fatalf("deleted paths = %+v, want both attachment paths", paths)
	}
	threadAttachments, err = s.ListAttachments(ctx, "thread", thread.ID)
	if err != nil {
		t.Fatalf("ListAttachments thread after delete error: %v", err)
	}
	commentAttachments, err = s.ListAttachments(ctx, "comment", comment.ID)
	if err != nil {
		t.Fatalf("ListAttachments comment after delete error: %v", err)
	}
	if len(threadAttachments) != 0 || len(commentAttachments) != 0 {
		t.Fatalf("attachments after owner delete = thread:%+v comment:%+v, want none", threadAttachments, commentAttachments)
	}
}

func TestDeleteCommentWithAttachmentPathsChecksOwnerAndSoftDeletesAttachments(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	attachment, err := s.CreateAttachment(ctx, CreateAttachmentInput{
		Name:        "comment.txt",
		Size:        1,
		MimeType:    "text/plain",
		StoragePath: "uploads/comment.txt",
	})
	if err != nil {
		t.Fatalf("CreateAttachment error: %v", err)
	}
	thread, err := s.CreateThread(ctx, CreateThreadInput{
		Type:          TypeText,
		Title:         "thread",
		Body:          "body",
		OwnerDeviceID: "dev_owner",
		AuthorName:    "owner",
	})
	if err != nil {
		t.Fatalf("CreateThread error: %v", err)
	}
	comment, err := s.CreateComment(ctx, thread.ID, CreateCommentInput{
		Body:          "comment",
		OwnerDeviceID: "dev_commenter",
		AuthorName:    "commenter",
		AttachmentIDs: []string{attachment.ID},
	})
	if err != nil {
		t.Fatalf("CreateComment error: %v", err)
	}
	paths, err := s.DeleteCommentWithAttachmentPaths(ctx, comment.ID, "dev_other", false)
	if err != ErrForbidden {
		t.Fatalf("DeleteCommentWithAttachmentPaths wrong device error = %v, want ErrForbidden", err)
	}
	if len(paths) != 0 {
		t.Fatalf("paths for wrong device = %+v, want none", paths)
	}
	if err := s.DeleteComment(ctx, comment.ID, "dev_other"); err != ErrForbidden {
		t.Fatalf("DeleteComment wrong device error = %v, want ErrForbidden", err)
	}
	attachments, err := s.ListAttachments(ctx, "comment", comment.ID)
	if err != nil {
		t.Fatalf("ListAttachments before owner delete error: %v", err)
	}
	if len(attachments) != 1 {
		t.Fatalf("attachments after forbidden delete = %+v, want one", attachments)
	}
	paths, err = s.DeleteCommentWithAttachmentPaths(ctx, comment.ID, "dev_commenter", false)
	if err != nil {
		t.Fatalf("DeleteCommentWithAttachmentPaths owner error: %v", err)
	}
	if !sameStrings(paths, []string{"uploads/comment.txt"}) {
		t.Fatalf("deleted paths = %+v, want comment attachment path", paths)
	}
	attachments, err = s.ListAttachments(ctx, "comment", comment.ID)
	if err != nil {
		t.Fatalf("ListAttachments after owner delete error: %v", err)
	}
	if len(attachments) != 0 {
		t.Fatalf("attachments after owner delete = %+v, want none", attachments)
	}
}

func TestHostFlagAllowsDeletingOthersThreadAndComment(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	thread, err := s.CreateThread(ctx, CreateThreadInput{
		Type:          TypeText,
		Title:         "host target",
		Body:          "body",
		OwnerDeviceID: "dev_owner",
		AuthorName:    "owner",
	})
	if err != nil {
		t.Fatalf("CreateThread error: %v", err)
	}
	first, err := s.CreateComment(ctx, thread.ID, CreateCommentInput{
		Body:          "first",
		OwnerDeviceID: "dev_commenter",
		AuthorName:    "commenter",
	})
	if err != nil {
		t.Fatalf("CreateComment error: %v", err)
	}
	if _, err := s.DeleteCommentWithAttachmentPaths(ctx, first.ID, "dev_host", false); err != ErrForbidden {
		t.Fatalf("non-host delete comment error = %v, want ErrForbidden", err)
	}
	if _, err := s.DeleteCommentWithAttachmentPaths(ctx, first.ID, "dev_host", true); err != nil {
		t.Fatalf("host delete comment error: %v", err)
	}
	second, err := s.CreateComment(ctx, thread.ID, CreateCommentInput{
		Body:          "second",
		OwnerDeviceID: "dev_commenter",
		AuthorName:    "commenter",
	})
	if err != nil {
		t.Fatalf("CreateComment second error: %v", err)
	}
	if second.Number != 2 {
		t.Fatalf("comment number after host delete = %d, want 2 (numbers stay sparse)", second.Number)
	}
	if _, err := s.UpdateThread(ctx, thread.ID, "dev_host", UpdateThreadInput{Title: "hijack", Body: "bad"}); err != ErrForbidden {
		t.Fatalf("host edit error = %v, want ErrForbidden (host has no edit rights)", err)
	}
	if _, err := s.DeleteThreadWithAttachmentPaths(ctx, thread.ID, "dev_host", false); err != ErrForbidden {
		t.Fatalf("non-host delete thread error = %v, want ErrForbidden", err)
	}
	if _, err := s.DeleteThreadWithAttachmentPaths(ctx, thread.ID, "dev_host", true); err != nil {
		t.Fatalf("host delete thread error: %v", err)
	}
	if _, err := s.GetThread(ctx, thread.ID); err != ErrNotFound {
		t.Fatalf("GetThread after host delete error = %v, want ErrNotFound", err)
	}
}

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := make(map[string]int, len(got))
	for _, value := range got {
		seen[value]++
	}
	for _, value := range want {
		if seen[value] == 0 {
			return false
		}
		seen[value]--
	}
	return true
}

func TestUpdateThreadRecordsEditHistory(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	thread, err := s.CreateThread(ctx, CreateThreadInput{
		Type: TypeMarkdown, Title: "v1 title", Body: "alpha\nbeta",
		OwnerDeviceID: "dev_owner", AuthorName: "owner",
	})
	if err != nil {
		t.Fatalf("CreateThread error: %v", err)
	}
	if _, err := s.UpdateThread(ctx, thread.ID, "dev_owner", UpdateThreadInput{
		Title: "v2 title", Body: "alpha\ngamma", AuthorName: "owner-renamed",
	}); err != nil {
		t.Fatalf("UpdateThread error: %v", err)
	}
	versions, err := s.ListThreadVersions(ctx, thread.ID)
	if err != nil {
		t.Fatalf("ListThreadVersions error: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("versions = %+v, want 2", versions)
	}
	current, initial := versions[0], versions[1]
	if current.Seq != 2 || !current.IsCurrent || current.Title != "v2 title" ||
		current.AuthorName != "owner-renamed" || current.AuthorDeviceID != "dev_owner" {
		t.Fatalf("current version mismatch: %+v", current)
	}
	if initial.Seq != 1 || initial.IsCurrent || initial.Title != "v1 title" ||
		initial.AuthorName != "owner" || !initial.CreatedAt.Equal(thread.CreatedAt) {
		t.Fatalf("initial version mismatch: %+v", initial)
	}

	older, newer, err := s.ThreadVersionContents(ctx, thread.ID, 2)
	if err != nil {
		t.Fatalf("ThreadVersionContents(2) error: %v", err)
	}
	if older.Title != "v1 title" || older.Body != "alpha\nbeta" ||
		newer.Title != "v2 title" || newer.Body != "alpha\ngamma" {
		t.Fatalf("contents(2) mismatch: older=%+v newer=%+v", older, newer)
	}
	older1, newer1, err := s.ThreadVersionContents(ctx, thread.ID, 1)
	if err != nil {
		t.Fatalf("ThreadVersionContents(1) error: %v", err)
	}
	if older1 != (VersionContent{}) || newer1.Body != "alpha\nbeta" {
		t.Fatalf("contents(1) mismatch: older=%+v newer=%+v", older1, newer1)
	}
	for _, seq := range []int{0, 3} {
		if _, _, err := s.ThreadVersionContents(ctx, thread.ID, seq); err != ErrNotFound {
			t.Fatalf("ThreadVersionContents(%d) error = %v, want ErrNotFound", seq, err)
		}
	}
	if _, err := s.ListThreadVersions(ctx, "thr_missing"); err != ErrNotFound {
		t.Fatalf("ListThreadVersions missing error = %v, want ErrNotFound", err)
	}
}

func TestUpdateThreadNoOpKeepsHistoryAndUpdatedAt(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	thread, err := s.CreateThread(ctx, CreateThreadInput{
		Type: TypeText, Title: "same", Body: "same body",
		OwnerDeviceID: "dev_owner", AuthorName: "owner",
	})
	if err != nil {
		t.Fatalf("CreateThread error: %v", err)
	}
	updated, err := s.UpdateThread(ctx, thread.ID, "dev_owner", UpdateThreadInput{
		Title: "same", Body: "same body", AuthorName: "owner",
	})
	if err != nil {
		t.Fatalf("UpdateThread no-op error: %v", err)
	}
	if !updated.UpdatedAt.Equal(thread.UpdatedAt) {
		t.Fatalf("no-op edit bumped updated_at: %v -> %v", thread.UpdatedAt, updated.UpdatedAt)
	}
	versions, err := s.ListThreadVersions(ctx, thread.ID)
	if err != nil {
		t.Fatalf("ListThreadVersions error: %v", err)
	}
	if len(versions) != 1 || !versions[0].IsCurrent || versions[0].Seq != 1 {
		t.Fatalf("versions after no-op = %+v, want single current version", versions)
	}
}

func TestFileThreadTitleEditRecordsHistory(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	thread, err := s.CreateThread(ctx, CreateThreadInput{
		Type: TypeFile, Title: "file v1", Body: "",
		OwnerDeviceID: "dev_owner", AuthorName: "owner",
	})
	if err != nil {
		t.Fatalf("CreateThread error: %v", err)
	}
	if _, err := s.UpdateThread(ctx, thread.ID, "dev_owner", UpdateThreadInput{
		Title: "file v2", Body: "ignored", AuthorName: "owner",
	}); err != nil {
		t.Fatalf("UpdateThread error: %v", err)
	}
	older, newer, err := s.ThreadVersionContents(ctx, thread.ID, 2)
	if err != nil {
		t.Fatalf("ThreadVersionContents error: %v", err)
	}
	if older.Title != "file v1" || newer.Title != "file v2" || older.Body != "" || newer.Body != "" {
		t.Fatalf("file thread contents mismatch: older=%+v newer=%+v", older, newer)
	}
}

func TestCommentDoesNotBumpThreadUpdatedAt(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	thread, err := s.CreateThread(ctx, CreateThreadInput{
		Type: TypeText, Title: "t", Body: "b",
		OwnerDeviceID: "dev_owner", AuthorName: "owner",
	})
	if err != nil {
		t.Fatalf("CreateThread error: %v", err)
	}
	comment, err := s.CreateComment(ctx, thread.ID, CreateCommentInput{
		Body: "hi", OwnerDeviceID: "dev_b", AuthorName: "b",
	})
	if err != nil {
		t.Fatalf("CreateComment error: %v", err)
	}
	got, err := s.GetThread(ctx, thread.ID)
	if err != nil {
		t.Fatalf("GetThread error: %v", err)
	}
	if !got.UpdatedAt.Equal(thread.UpdatedAt) {
		t.Fatalf("comment bumped updated_at: %v -> %v", thread.UpdatedAt, got.UpdatedAt)
	}
	// 一覧の最終更新はコメント側から計算される（updated_at に依存しない）
	if !got.LatestAt.Equal(comment.CreatedAt) || got.LatestActor != "b" {
		t.Fatalf("latest activity mismatch: %+v", got)
	}
}

func TestConcurrentIdenticalEditsCreateOneSnapshot(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	thread, err := s.CreateThread(ctx, CreateThreadInput{
		Type: TypeText, Title: "t0", Body: "b0",
		OwnerDeviceID: "dev_owner", AuthorName: "owner",
	})
	if err != nil {
		t.Fatalf("CreateThread error: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.UpdateThread(ctx, thread.ID, "dev_owner", UpdateThreadInput{
				Title: "t1", Body: "b1", AuthorName: "owner",
			}); err != nil {
				t.Errorf("UpdateThread error: %v", err)
			}
		}()
	}
	wg.Wait()
	versions, err := s.ListThreadVersions(ctx, thread.ID)
	if err != nil {
		t.Fatalf("ListThreadVersions error: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("versions after concurrent identical edits = %d, want 2 (one real transition)", len(versions))
	}
}

func TestMigrationAddsThreadVersionColumnToLegacyDB(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "legacy.sqlite")

	// thread_version カラムのない旧スキーマDBを用意する
	legacy, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	_, err = legacy.Exec(`
CREATE TABLE comments (
	id TEXT PRIMARY KEY,
	thread_id TEXT NOT NULL,
	number INTEGER NOT NULL,
	body TEXT NOT NULL DEFAULT '',
	owner_device_id TEXT NOT NULL,
	author_name TEXT NOT NULL,
	created_at TEXT NOT NULL,
	deleted_at TEXT
);`)
	if err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	// Open がマイグレーションでカラムを追加する
	s, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open error: %v", err)
	}
	hasColumn := func() bool {
		rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(comments)`)
		if err != nil {
			t.Fatalf("pragma error: %v", err)
		}
		defer rows.Close()
		for rows.Next() {
			var cid, notNull, pk int
			var name, typ string
			var dflt sql.NullString
			if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
				t.Fatalf("pragma scan error: %v", err)
			}
			if name == "thread_version" {
				return true
			}
		}
		return false
	}
	if !hasColumn() {
		t.Fatalf("thread_version column not added by migration")
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	// 再オープンしても冪等（重複ALTERでエラーにならない）
	s2, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen error: %v", err)
	}
	_ = s2.Close()
}

func TestCommentRecordsThreadVersion(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	thread, err := s.CreateThread(ctx, CreateThreadInput{
		Type:          TypeMarkdown,
		Title:         "versioned",
		Body:          "# v1",
		OwnerDeviceID: "dev_a",
		AuthorName:    "wata",
	})
	if err != nil {
		t.Fatalf("CreateThread error: %v", err)
	}

	// 未編集スレ（v1）へのコメントは threadVersion=1
	c1, err := s.CreateComment(ctx, thread.ID, CreateCommentInput{
		Body: "first", OwnerDeviceID: "dev_b", AuthorName: "poo",
	})
	if err != nil {
		t.Fatalf("CreateComment error: %v", err)
	}
	if c1.ThreadVersion != 1 {
		t.Fatalf("c1.ThreadVersion = %d, want 1", c1.ThreadVersion)
	}

	// 編集後（v2）のコメントは threadVersion=2
	if _, err := s.UpdateThread(ctx, thread.ID, "dev_a", UpdateThreadInput{
		Title: "versioned v2", Body: "# v2", AuthorName: "wata",
	}); err != nil {
		t.Fatalf("UpdateThread error: %v", err)
	}
	c2, err := s.CreateComment(ctx, thread.ID, CreateCommentInput{
		Body: "second", OwnerDeviceID: "dev_b", AuthorName: "poo",
	})
	if err != nil {
		t.Fatalf("CreateComment error: %v", err)
	}
	if c2.ThreadVersion != 2 {
		t.Fatalf("c2.ThreadVersion = %d, want 2", c2.ThreadVersion)
	}

	// 機能導入前のコメント（thread_version が NULL）は 0 で返る
	if _, err := s.db.ExecContext(ctx, `UPDATE comments SET thread_version=NULL WHERE id=?`, c1.ID); err != nil {
		t.Fatalf("null out version: %v", err)
	}
	comments, err := s.ListComments(ctx, thread.ID)
	if err != nil {
		t.Fatalf("ListComments error: %v", err)
	}
	if len(comments) != 2 {
		t.Fatalf("comment count = %d, want 2", len(comments))
	}
	if comments[0].ThreadVersion != 0 {
		t.Fatalf("legacy comment ThreadVersion = %d, want 0", comments[0].ThreadVersion)
	}
	if comments[1].ThreadVersion != 2 {
		t.Fatalf("comments[1].ThreadVersion = %d, want 2", comments[1].ThreadVersion)
	}
}
