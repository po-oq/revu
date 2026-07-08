package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func Open(ctx context.Context, path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.applySchema(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) applySchema(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
PRAGMA foreign_keys = ON;
CREATE TABLE IF NOT EXISTS threads (
	id TEXT PRIMARY KEY,
	type TEXT NOT NULL,
	title TEXT NOT NULL,
	body TEXT NOT NULL DEFAULT '',
	owner_device_id TEXT NOT NULL,
	author_name TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	deleted_at TEXT
);
CREATE TABLE IF NOT EXISTS comments (
	id TEXT PRIMARY KEY,
	thread_id TEXT NOT NULL,
	number INTEGER NOT NULL,
	body TEXT NOT NULL DEFAULT '',
	owner_device_id TEXT NOT NULL,
	author_name TEXT NOT NULL,
	created_at TEXT NOT NULL,
	deleted_at TEXT,
	FOREIGN KEY(thread_id) REFERENCES threads(id)
);
CREATE UNIQUE INDEX IF NOT EXISTS comments_thread_number_idx ON comments(thread_id, number);
CREATE TABLE IF NOT EXISTS attachments (
	id TEXT PRIMARY KEY,
	owner_kind TEXT NOT NULL,
	owner_id TEXT NOT NULL,
	name TEXT NOT NULL,
	size INTEGER NOT NULL,
	mime_type TEXT NOT NULL,
	storage_path TEXT NOT NULL,
	created_at TEXT NOT NULL,
	deleted_at TEXT
);
CREATE TABLE IF NOT EXISTS thread_edits (
	id TEXT PRIMARY KEY,
	thread_id TEXT NOT NULL,
	seq INTEGER NOT NULL,
	title TEXT NOT NULL,
	body TEXT NOT NULL DEFAULT '',
	edited_at TEXT NOT NULL,
	editor_device_id TEXT NOT NULL,
	editor_name TEXT NOT NULL,
	FOREIGN KEY(thread_id) REFERENCES threads(id)
);
CREATE UNIQUE INDEX IF NOT EXISTS thread_edits_thread_seq_idx ON thread_edits(thread_id, seq);
`)
	if err != nil {
		return err
	}
	return s.ensureCommentThreadVersionColumn(ctx)
}

// ensureCommentThreadVersionColumn adds comments.thread_version for databases
// created before the column existed. CREATE TABLE IF NOT EXISTS does not add
// columns to existing tables, so this must run on every open (idempotent).
func (s *Store) ensureCommentThreadVersionColumn(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(comments)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, pk int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return err
		}
		if name == "thread_version" {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `ALTER TABLE comments ADD COLUMN thread_version INTEGER`)
	return err
}

func newID(prefix string) string {
	var buf [12]byte
	if _, err := rand.Read(buf[:]); err != nil {
		panic(err)
	}
	return prefix + "_" + hex.EncodeToString(buf[:])
}

func nowUTC() time.Time {
	return time.Now().UTC().Truncate(time.Second)
}

func validateIdentity(deviceID, author string) error {
	if strings.TrimSpace(deviceID) == "" || strings.TrimSpace(author) == "" {
		return ErrInvalid
	}
	return nil
}

func (s *Store) CreateThread(ctx context.Context, in CreateThreadInput) (Thread, error) {
	if err := validateIdentity(in.OwnerDeviceID, in.AuthorName); err != nil {
		return Thread{}, err
	}
	if in.Type != TypeMarkdown && in.Type != TypeHTML && in.Type != TypeText && in.Type != TypeFile {
		return Thread{}, ErrInvalid
	}
	if strings.TrimSpace(in.Title) == "" {
		return Thread{}, ErrInvalid
	}
	if in.Type != TypeFile && strings.TrimSpace(in.Body) == "" {
		return Thread{}, ErrInvalid
	}
	now := nowUTC()
	thread := Thread{
		ID:            newID("thr"),
		Type:          in.Type,
		Title:         strings.TrimSpace(in.Title),
		Body:          in.Body,
		OwnerDeviceID: in.OwnerDeviceID,
		AuthorName:    in.AuthorName,
		CreatedAt:     now,
		UpdatedAt:     now,
		LatestActor:   in.AuthorName,
		LatestAt:      now,
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Thread{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO threads (id,type,title,body,owner_device_id,author_name,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?)`,
		thread.ID, thread.Type, thread.Title, thread.Body, thread.OwnerDeviceID, thread.AuthorName, thread.CreatedAt.Format(time.RFC3339), thread.UpdatedAt.Format(time.RFC3339)); err != nil {
		return Thread{}, err
	}
	if err := attachExisting(ctx, tx, "thread", thread.ID, in.AttachmentIDs); err != nil {
		return Thread{}, err
	}
	if err := tx.Commit(); err != nil {
		return Thread{}, err
	}
	return s.GetThread(ctx, thread.ID)
}

func (s *Store) ListThreads(ctx context.Context) ([]Thread, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT t.id,t.type,t.title,t.body,t.owner_device_id,t.author_name,t.created_at,t.updated_at,
       COUNT(c.id) AS comment_count,
       COALESCE((SELECT c2.author_name FROM comments c2 WHERE c2.thread_id=t.id AND c2.deleted_at IS NULL ORDER BY c2.number DESC LIMIT 1), t.author_name) AS latest_actor,
       COALESCE((SELECT c3.created_at FROM comments c3 WHERE c3.thread_id=t.id AND c3.deleted_at IS NULL ORDER BY c3.number DESC LIMIT 1), t.updated_at) AS latest_at,
       (SELECT COUNT(*)+1 FROM thread_edits e WHERE e.thread_id=t.id) AS current_version
FROM threads t
LEFT JOIN comments c ON c.thread_id=t.id AND c.deleted_at IS NULL
WHERE t.deleted_at IS NULL
GROUP BY t.id
ORDER BY latest_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Thread
	for rows.Next() {
		thread, err := scanThread(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, thread)
	}
	return out, rows.Err()
}

func (s *Store) GetThread(ctx context.Context, id string) (Thread, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT t.id,t.type,t.title,t.body,t.owner_device_id,t.author_name,t.created_at,t.updated_at,
       (SELECT COUNT(*) FROM comments c WHERE c.thread_id=t.id AND c.deleted_at IS NULL),
       COALESCE((SELECT c2.author_name FROM comments c2 WHERE c2.thread_id=t.id AND c2.deleted_at IS NULL ORDER BY c2.number DESC LIMIT 1), t.author_name),
       COALESCE((SELECT c3.created_at FROM comments c3 WHERE c3.thread_id=t.id AND c3.deleted_at IS NULL ORDER BY c3.number DESC LIMIT 1), t.updated_at),
       (SELECT COUNT(*)+1 FROM thread_edits e WHERE e.thread_id=t.id)
FROM threads t
WHERE t.id=? AND t.deleted_at IS NULL`, id)
	thread, err := scanThread(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Thread{}, ErrNotFound
	}
	if err != nil {
		return Thread{}, err
	}
	comments, err := s.ListComments(ctx, id)
	if err != nil {
		return Thread{}, err
	}
	attachments, err := s.ListAttachments(ctx, "thread", id)
	if err != nil {
		return Thread{}, err
	}
	thread.Comments = comments
	thread.Attachments = attachments
	return thread, nil
}

func (s *Store) UpdateThread(ctx context.Context, id, deviceID string, in UpdateThreadInput) (Thread, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Thread{}, err
	}
	defer tx.Rollback()
	// Read the values used for the ownership/no-op/snapshot decisions inside
	// the transaction; a read taken outside can be stale under concurrent
	// edits and would snapshot duplicate content.
	var typ, curTitle, curBody, owner string
	err = tx.QueryRowContext(ctx, `SELECT type,title,body,owner_device_id FROM threads WHERE id=? AND deleted_at IS NULL`, id).
		Scan(&typ, &curTitle, &curBody, &owner)
	if errors.Is(err, sql.ErrNoRows) {
		return Thread{}, ErrNotFound
	}
	if err != nil {
		return Thread{}, err
	}
	if owner != deviceID {
		return Thread{}, ErrForbidden
	}
	if err := validateIdentity(deviceID, in.AuthorName); err != nil {
		return Thread{}, err
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return Thread{}, ErrInvalid
	}
	body := in.Body
	if ThreadType(typ) == TypeFile {
		body = curBody
	} else if strings.TrimSpace(body) == "" {
		return Thread{}, ErrInvalid
	}
	if title == curTitle && body == curBody {
		// A no-op edit writes nothing: no snapshot and no updated_at bump
		// (the UI treats updatedAt != createdAt as "has been edited").
		// Release the transaction before GetThread; the pool has a single
		// connection, so querying s.db while tx holds it would deadlock.
		if err := tx.Rollback(); err != nil {
			return Thread{}, err
		}
		return s.GetThread(ctx, id)
	}
	now := nowUTC()
	var nextSeq int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),0)+1 FROM thread_edits WHERE thread_id=?`, id).Scan(&nextSeq); err != nil {
		return Thread{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO thread_edits (id,thread_id,seq,title,body,edited_at,editor_device_id,editor_name) VALUES (?,?,?,?,?,?,?,?)`,
		newID("edt"), id, nextSeq, curTitle, curBody, now.Format(time.RFC3339), deviceID, in.AuthorName); err != nil {
		return Thread{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE threads SET title=?, body=?, updated_at=? WHERE id=? AND deleted_at IS NULL`,
		title, body, now.Format(time.RFC3339), id); err != nil {
		return Thread{}, err
	}
	if err := tx.Commit(); err != nil {
		return Thread{}, err
	}
	return s.GetThread(ctx, id)
}

func (s *Store) DeleteThread(ctx context.Context, id, deviceID string) error {
	_, err := s.DeleteThreadWithAttachmentPaths(ctx, id, deviceID, false)
	return err
}

// DeleteThreadWithAttachmentPaths soft-deletes a thread with its comments and
// attachments. asHost bypasses the owner check; the host may delete any post
// but never gains edit rights.
func (s *Store) DeleteThreadWithAttachmentPaths(ctx context.Context, id, deviceID string, asHost bool) ([]string, error) {
	now := nowUTC().Format(time.RFC3339)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var owner string
	err = tx.QueryRowContext(ctx, `SELECT owner_device_id FROM threads WHERE id=? AND deleted_at IS NULL`, id).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if !asHost && owner != deviceID {
		return nil, ErrForbidden
	}
	paths, err := threadAttachmentPaths(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE threads SET deleted_at=? WHERE id=?`, now, id); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE comments SET deleted_at=? WHERE thread_id=? AND deleted_at IS NULL`, now, id); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE attachments SET deleted_at=? WHERE (owner_kind='thread' AND owner_id=?) OR (owner_kind='comment' AND owner_id IN (SELECT id FROM comments WHERE thread_id=?))`, now, id, id); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return paths, nil
}

func (s *Store) CreateComment(ctx context.Context, threadID string, in CreateCommentInput) (Comment, error) {
	if err := validateIdentity(in.OwnerDeviceID, in.AuthorName); err != nil {
		return Comment{}, err
	}
	if strings.TrimSpace(in.Body) == "" && len(in.AttachmentIDs) == 0 {
		return Comment{}, ErrInvalid
	}
	if _, err := s.GetThread(ctx, threadID); err != nil {
		return Comment{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Comment{}, err
	}
	defer tx.Rollback()
	var next int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(number), 0) + 1 FROM comments WHERE thread_id=?`, threadID).Scan(&next); err != nil {
		return Comment{}, err
	}
	// コメント時点のスレバージョン。thread_edits は編集前スナップショットの
	// 集まりなので、現在バージョン = 行数 + 1
	var version int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*)+1 FROM thread_edits WHERE thread_id=?`, threadID).Scan(&version); err != nil {
		return Comment{}, err
	}
	now := nowUTC()
	comment := Comment{
		ID:            newID("com"),
		ThreadID:      threadID,
		Number:        next,
		Body:          in.Body,
		OwnerDeviceID: in.OwnerDeviceID,
		AuthorName:    in.AuthorName,
		CreatedAt:     now,
		ThreadVersion: version,
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO comments (id,thread_id,number,body,owner_device_id,author_name,created_at,thread_version) VALUES (?,?,?,?,?,?,?,?)`,
		comment.ID, comment.ThreadID, comment.Number, comment.Body, comment.OwnerDeviceID, comment.AuthorName, comment.CreatedAt.Format(time.RFC3339), comment.ThreadVersion); err != nil {
		return Comment{}, err
	}
	if err := attachExisting(ctx, tx, "comment", comment.ID, in.AttachmentIDs); err != nil {
		return Comment{}, err
	}
	if err := tx.Commit(); err != nil {
		return Comment{}, err
	}
	return s.getComment(ctx, comment.ID)
}

func (s *Store) ListComments(ctx context.Context, threadID string) ([]Comment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,thread_id,number,body,owner_device_id,author_name,created_at,thread_version FROM comments WHERE thread_id=? AND deleted_at IS NULL ORDER BY number ASC`, threadID)
	if err != nil {
		return nil, err
	}
	var out []Comment
	for rows.Next() {
		var c Comment
		var created string
		var version sql.NullInt64
		if err := rows.Scan(&c.ID, &c.ThreadID, &c.Number, &c.Body, &c.OwnerDeviceID, &c.AuthorName, &created, &version); err != nil {
			_ = rows.Close()
			return nil, err
		}
		parsed, err := time.Parse(time.RFC3339, created)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		c.CreatedAt = parsed
		c.ThreadVersion = int(version.Int64)
		out = append(out, c)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		attachments, err := s.ListAttachments(ctx, "comment", out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Attachments = attachments
	}
	return out, nil
}

func (s *Store) DeleteComment(ctx context.Context, id, deviceID string) error {
	_, err := s.DeleteCommentWithAttachmentPaths(ctx, id, deviceID, false)
	return err
}

// DeleteCommentWithAttachmentPaths soft-deletes a comment and its attachments.
// asHost bypasses the owner check (see DeleteThreadWithAttachmentPaths).
func (s *Store) DeleteCommentWithAttachmentPaths(ctx context.Context, id, deviceID string, asHost bool) ([]string, error) {
	now := nowUTC().Format(time.RFC3339)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var owner string
	err = tx.QueryRowContext(ctx, `SELECT owner_device_id FROM comments WHERE id=? AND deleted_at IS NULL`, id).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if !asHost && owner != deviceID {
		return nil, ErrForbidden
	}
	paths, err := commentAttachmentPaths(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE comments SET deleted_at=? WHERE id=?`, now, id); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE attachments SET deleted_at=? WHERE owner_kind='comment' AND owner_id=?`, now, id); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return paths, nil
}

func (s *Store) CreateAttachment(ctx context.Context, in CreateAttachmentInput) (Attachment, error) {
	if strings.TrimSpace(in.Name) == "" || strings.TrimSpace(in.StoragePath) == "" || in.Size < 0 {
		return Attachment{}, ErrInvalid
	}
	if (in.OwnerKind == "") != (in.OwnerID == "") {
		return Attachment{}, ErrInvalid
	}
	if in.OwnerKind != "" && in.OwnerKind != "thread" && in.OwnerKind != "comment" {
		return Attachment{}, ErrInvalid
	}
	now := nowUTC()
	att := Attachment{
		ID:          newID("att"),
		OwnerKind:   in.OwnerKind,
		OwnerID:     in.OwnerID,
		Name:        in.Name,
		Size:        in.Size,
		MimeType:    in.MimeType,
		StoragePath: in.StoragePath,
		CreatedAt:   now,
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO attachments (id,owner_kind,owner_id,name,size,mime_type,storage_path,created_at) VALUES (?,?,?,?,?,?,?,?)`,
		att.ID, att.OwnerKind, att.OwnerID, att.Name, att.Size, att.MimeType, att.StoragePath, att.CreatedAt.Format(time.RFC3339)); err != nil {
		return Attachment{}, err
	}
	att.DownloadURL = fmt.Sprintf("/api/attachments/%s/download", att.ID)
	return att, nil
}

func (s *Store) AttachmentByID(ctx context.Context, id string) (Attachment, error) {
	var att Attachment
	var created string
	err := s.db.QueryRowContext(ctx, `SELECT id,owner_kind,owner_id,name,size,mime_type,storage_path,created_at FROM attachments WHERE id=? AND deleted_at IS NULL`, id).
		Scan(&att.ID, &att.OwnerKind, &att.OwnerID, &att.Name, &att.Size, &att.MimeType, &att.StoragePath, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return Attachment{}, ErrNotFound
	}
	if err != nil {
		return Attachment{}, err
	}
	createdAt, err := time.Parse(time.RFC3339, created)
	if err != nil {
		return Attachment{}, err
	}
	att.CreatedAt = createdAt
	att.DownloadURL = fmt.Sprintf("/api/attachments/%s/download", att.ID)
	return att, nil
}

func (s *Store) ListAttachments(ctx context.Context, ownerKind, ownerID string) ([]Attachment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,owner_kind,owner_id,name,size,mime_type,storage_path,created_at FROM attachments WHERE owner_kind=? AND owner_id=? AND deleted_at IS NULL ORDER BY created_at ASC`, ownerKind, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Attachment
	for rows.Next() {
		var att Attachment
		var created string
		if err := rows.Scan(&att.ID, &att.OwnerKind, &att.OwnerID, &att.Name, &att.Size, &att.MimeType, &att.StoragePath, &created); err != nil {
			return nil, err
		}
		parsed, err := time.Parse(time.RFC3339, created)
		if err != nil {
			return nil, err
		}
		att.CreatedAt = parsed
		att.DownloadURL = fmt.Sprintf("/api/attachments/%s/download", att.ID)
		out = append(out, att)
	}
	return out, rows.Err()
}

type threadScanner interface {
	Scan(dest ...any) error
}

func scanThread(row threadScanner) (Thread, error) {
	var thread Thread
	var typ string
	var created string
	var updated string
	var latest string
	if err := row.Scan(&thread.ID, &typ, &thread.Title, &thread.Body, &thread.OwnerDeviceID, &thread.AuthorName, &created, &updated, &thread.CommentCount, &thread.LatestActor, &latest, &thread.CurrentVersion); err != nil {
		return Thread{}, err
	}
	createdAt, err := time.Parse(time.RFC3339, created)
	if err != nil {
		return Thread{}, err
	}
	updatedAt, err := time.Parse(time.RFC3339, updated)
	if err != nil {
		return Thread{}, err
	}
	latestAt, err := time.Parse(time.RFC3339, latest)
	if err != nil {
		return Thread{}, err
	}
	thread.Type = ThreadType(typ)
	thread.CreatedAt = createdAt
	thread.UpdatedAt = updatedAt
	thread.LatestAt = latestAt
	return thread, nil
}

func (s *Store) getComment(ctx context.Context, id string) (Comment, error) {
	var comment Comment
	var created string
	var version sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT id,thread_id,number,body,owner_device_id,author_name,created_at,thread_version FROM comments WHERE id=? AND deleted_at IS NULL`, id).
		Scan(&comment.ID, &comment.ThreadID, &comment.Number, &comment.Body, &comment.OwnerDeviceID, &comment.AuthorName, &created, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return Comment{}, ErrNotFound
	}
	if err != nil {
		return Comment{}, err
	}
	createdAt, err := time.Parse(time.RFC3339, created)
	if err != nil {
		return Comment{}, err
	}
	comment.CreatedAt = createdAt
	comment.ThreadVersion = int(version.Int64)
	attachments, err := s.ListAttachments(ctx, "comment", comment.ID)
	if err != nil {
		return Comment{}, err
	}
	comment.Attachments = attachments
	return comment, nil
}

func attachExisting(ctx context.Context, tx *sql.Tx, ownerKind, ownerID string, ids []string) error {
	for _, id := range ids {
		res, err := tx.ExecContext(ctx, `UPDATE attachments SET owner_kind=?, owner_id=? WHERE id=? AND owner_kind='' AND owner_id='' AND deleted_at IS NULL`, ownerKind, ownerID, id)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrNotFound
		}
	}
	return nil
}

func threadAttachmentPaths(ctx context.Context, tx *sql.Tx, threadID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT storage_path FROM attachments WHERE deleted_at IS NULL AND ((owner_kind='thread' AND owner_id=?) OR (owner_kind='comment' AND owner_id IN (SELECT id FROM comments WHERE thread_id=?))) ORDER BY created_at ASC`, threadID, threadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAttachmentPaths(rows)
}

func commentAttachmentPaths(ctx context.Context, tx *sql.Tx, commentID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT storage_path FROM attachments WHERE deleted_at IS NULL AND owner_kind='comment' AND owner_id=? ORDER BY created_at ASC`, commentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAttachmentPaths(rows)
}

func scanAttachmentPaths(rows *sql.Rows) ([]string, error) {
	var paths []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return paths, nil
}

type threadEdit struct {
	Seq            int
	Title          string
	Body           string
	EditedAt       time.Time
	EditorDeviceID string
	EditorName     string
}

func (s *Store) listThreadEdits(ctx context.Context, threadID string) ([]threadEdit, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT seq,title,body,edited_at,editor_device_id,editor_name FROM thread_edits WHERE thread_id=? ORDER BY seq ASC`, threadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []threadEdit
	for rows.Next() {
		var edit threadEdit
		var edited string
		if err := rows.Scan(&edit.Seq, &edit.Title, &edit.Body, &edited, &edit.EditorDeviceID, &edit.EditorName); err != nil {
			return nil, err
		}
		parsed, err := time.Parse(time.RFC3339, edited)
		if err != nil {
			return nil, err
		}
		edit.EditedAt = parsed
		out = append(out, edit)
	}
	return out, rows.Err()
}

// ListThreadVersions returns every generation of a thread, newest first.
// Snapshot k holds the content of generation k; the threads row is the
// newest generation. Generation k's creation time is the thread creation
// time for k=1, otherwise snapshot k-1's edited_at.
func (s *Store) ListThreadVersions(ctx context.Context, threadID string) ([]ThreadVersion, error) {
	thread, err := s.GetThread(ctx, threadID)
	if err != nil {
		return nil, err
	}
	edits, err := s.listThreadEdits(ctx, threadID)
	if err != nil {
		return nil, err
	}
	total := len(edits) + 1
	versions := make([]ThreadVersion, 0, total)
	for k := total; k >= 1; k-- {
		version := ThreadVersion{Seq: k}
		if k == 1 {
			version.AuthorName = thread.AuthorName
			version.AuthorDeviceID = thread.OwnerDeviceID
			version.CreatedAt = thread.CreatedAt
		} else {
			prev := edits[k-2]
			version.AuthorName = prev.EditorName
			version.AuthorDeviceID = prev.EditorDeviceID
			version.CreatedAt = prev.EditedAt
		}
		if k <= len(edits) {
			version.Title = edits[k-1].Title
		} else {
			version.Title = thread.Title
			version.IsCurrent = true
		}
		versions = append(versions, version)
	}
	return versions, nil
}

// ThreadVersionContents returns the contents of generation seq and its
// predecessor. For seq=1 the older content is zero (diff shows the initial
// version as pure additions). Out-of-range seq is ErrNotFound.
func (s *Store) ThreadVersionContents(ctx context.Context, threadID string, seq int) (older, newer VersionContent, err error) {
	thread, err := s.GetThread(ctx, threadID)
	if err != nil {
		return VersionContent{}, VersionContent{}, err
	}
	edits, err := s.listThreadEdits(ctx, threadID)
	if err != nil {
		return VersionContent{}, VersionContent{}, err
	}
	total := len(edits) + 1
	if seq < 1 || seq > total {
		return VersionContent{}, VersionContent{}, ErrNotFound
	}
	content := func(k int) VersionContent {
		if k <= len(edits) {
			return VersionContent{Title: edits[k-1].Title, Body: edits[k-1].Body}
		}
		return VersionContent{Title: thread.Title, Body: thread.Body}
	}
	newer = content(seq)
	if seq > 1 {
		older = content(seq - 1)
	}
	return older, newer, nil
}
