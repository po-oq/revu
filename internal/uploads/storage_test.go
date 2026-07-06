package uploads

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestSaveGeneratesSafePath(t *testing.T) {
	root := t.TempDir()
	st := NewStorage(filepath.Join(root, "uploads"), 1024)

	saved, err := st.Save(`..\bad/name.png`, "image/png", strings.NewReader("png data"), int64(len("png data")))
	if err != nil {
		t.Fatalf("Save error: %v", err)
	}

	if st.RootDir() != root {
		t.Fatalf("RootDir = %q, want %q", st.RootDir(), root)
	}
	if saved.OriginalName != "bad_name.png" {
		t.Fatalf("OriginalName = %q, want %q", saved.OriginalName, "bad_name.png")
	}
	if saved.MimeType != "image/png" {
		t.Fatalf("MimeType = %q, want image/png", saved.MimeType)
	}
	if saved.Size != int64(len("png data")) {
		t.Fatalf("Size = %d, want %d", saved.Size, len("png data"))
	}
	if matched := regexp.MustCompile(`^uploads/[0-9a-f]{24}_bad_name\.png$`).MatchString(saved.RelativePath); !matched {
		t.Fatalf("RelativePath = %q, want uploads/<24 hex>_bad_name.png", saved.RelativePath)
	}
	if strings.Contains(saved.RelativePath, `\`) {
		t.Fatalf("RelativePath = %q, want slash-separated path", saved.RelativePath)
	}
	data, err := os.ReadFile(filepath.Join(st.RootDir(), saved.RelativePath))
	if err != nil {
		t.Fatalf("ReadFile saved file error: %v", err)
	}
	if string(data) != "png data" {
		t.Fatalf("saved content = %q, want png data", data)
	}
}

func TestMaxFileBytes(t *testing.T) {
	st := NewStorage(filepath.Join(t.TempDir(), "uploads"), 123)

	if got := st.MaxFileBytes(); got != 123 {
		t.Fatalf("MaxFileBytes = %d, want 123", got)
	}
}

func TestSaveRejectsTooLarge(t *testing.T) {
	st := NewStorage(filepath.Join(t.TempDir(), "uploads"), 4)

	if _, err := st.Save("declared.txt", "text/plain", strings.NewReader("data"), 5); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Save declared too large error = %v, want ErrTooLarge", err)
	}

	saved, err := st.Save("ok.txt", "text/plain", strings.NewReader("1234"), 4)
	if err != nil {
		t.Fatalf("Save max-sized file error: %v", err)
	}
	if saved.Size != 4 {
		t.Fatalf("max-sized file Size = %d, want 4", saved.Size)
	}

	if _, err := st.Save("streamed.txt", "text/plain", strings.NewReader("12345"), -1); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Save streamed too large error = %v, want ErrTooLarge", err)
	}
	entries, err := os.ReadDir(filepath.Join(st.RootDir(), "uploads"))
	if err != nil {
		t.Fatalf("ReadDir uploads error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("uploads entries after rejected saves = %d, want only successful file", len(entries))
	}

	if _, err := st.Save("broken.txt", "text/plain", errReader{}, -1); err == nil || errors.Is(err, ErrTooLarge) {
		t.Fatalf("Save streaming error = %v, want underlying read error", err)
	}
	entries, err = os.ReadDir(filepath.Join(st.RootDir(), "uploads"))
	if err != nil {
		t.Fatalf("ReadDir uploads after streaming error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("uploads entries after streaming error = %d, want only successful file", len(entries))
	}
}

func TestOpenAndDelete(t *testing.T) {
	st := NewStorage(filepath.Join(t.TempDir(), "uploads"), 1024)
	saved, err := st.Save("note.txt", "text/plain", bytes.NewBufferString("hello"), int64(len("hello")))
	if err != nil {
		t.Fatalf("Save error: %v", err)
	}

	rc, err := st.Open(saved.RelativePath)
	if err != nil {
		t.Fatalf("Open error: %v", err)
	}
	data, err := io.ReadAll(rc)
	closeErr := rc.Close()
	if err != nil {
		t.Fatalf("ReadAll error: %v", err)
	}
	if closeErr != nil {
		t.Fatalf("Close error: %v", closeErr)
	}
	if string(data) != "hello" {
		t.Fatalf("Open content = %q, want hello", data)
	}

	for _, badPath := range []string{filepath.Join(st.RootDir(), saved.RelativePath), "../escape.txt", "uploads/../escape.txt"} {
		if _, err := st.Open(badPath); err == nil {
			t.Fatalf("Open(%q) error = nil, want rejection", badPath)
		}
		if err := st.Delete(badPath); err == nil {
			t.Fatalf("Delete(%q) error = nil, want rejection", badPath)
		}
	}

	if err := st.Delete(saved.RelativePath); err != nil {
		t.Fatalf("Delete error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(st.RootDir(), saved.RelativePath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Stat after Delete error = %v, want not exist", err)
	}
	if err := st.Delete(saved.RelativePath); err != nil {
		t.Fatalf("Delete missing file error = %v, want nil", err)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) {
	return 0, errors.New("read failed")
}
