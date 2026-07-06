package store

import (
	"errors"
	"time"
)

type ThreadType string

const (
	TypeMarkdown ThreadType = "markdown"
	TypeHTML     ThreadType = "html"
	TypeText     ThreadType = "text"
	TypeFile     ThreadType = "file"
)

var (
	ErrNotFound  = errors.New("not found")
	ErrForbidden = errors.New("forbidden")
	ErrInvalid   = errors.New("invalid input")
)

type Thread struct {
	ID            string       `json:"id"`
	Type          ThreadType   `json:"type"`
	Title         string       `json:"title"`
	Body          string       `json:"body"`
	OwnerDeviceID string       `json:"ownerDeviceId"`
	AuthorName    string       `json:"authorName"`
	CreatedAt     time.Time    `json:"createdAt"`
	UpdatedAt     time.Time    `json:"updatedAt"`
	CommentCount  int          `json:"commentCount"`
	LatestActor   string       `json:"latestActor"`
	LatestAt      time.Time    `json:"latestAt"`
	Attachments   []Attachment `json:"attachments,omitempty"`
	Comments      []Comment    `json:"comments,omitempty"`
}

type Comment struct {
	ID            string       `json:"id"`
	ThreadID      string       `json:"threadId"`
	Number        int          `json:"number"`
	Body          string       `json:"body"`
	OwnerDeviceID string       `json:"ownerDeviceId"`
	AuthorName    string       `json:"authorName"`
	CreatedAt     time.Time    `json:"createdAt"`
	Attachments   []Attachment `json:"attachments,omitempty"`
}

type Attachment struct {
	ID          string    `json:"id"`
	OwnerKind   string    `json:"ownerKind"`
	OwnerID     string    `json:"ownerId"`
	Name        string    `json:"name"`
	Size        int64     `json:"size"`
	MimeType    string    `json:"mimeType"`
	StoragePath string    `json:"-"`
	DownloadURL string    `json:"downloadUrl"`
	CreatedAt   time.Time `json:"createdAt"`
}

type CreateThreadInput struct {
	Type          ThreadType
	Title         string
	Body          string
	OwnerDeviceID string
	AuthorName    string
	AttachmentIDs []string
}

type UpdateThreadInput struct {
	Title      string
	Body       string
	AuthorName string
}

// ThreadVersion is one generation of a thread's content for the history
// list. Seq 1 is the initial version; the highest Seq is the current one.
type ThreadVersion struct {
	Seq            int       `json:"seq"`
	IsCurrent      bool      `json:"isCurrent"`
	Title          string    `json:"title"`
	AuthorName     string    `json:"authorName"`
	AuthorDeviceID string    `json:"authorDeviceId"`
	CreatedAt      time.Time `json:"createdAt"`
}

// VersionContent is the full content of one generation, used for diffing.
type VersionContent struct {
	Title string
	Body  string
}

type CreateCommentInput struct {
	Body          string
	OwnerDeviceID string
	AuthorName    string
	AttachmentIDs []string
}

type CreateAttachmentInput struct {
	OwnerKind   string
	OwnerID     string
	Name        string
	Size        int64
	MimeType    string
	StoragePath string
}
