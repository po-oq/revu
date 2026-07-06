package uploads

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

var ErrTooLarge = errors.New("upload too large")

type SavedFile struct {
	OriginalName string
	MimeType     string
	Size         int64
	RelativePath string
}

type Storage struct {
	rootDir      string
	uploadsDir   string
	maxFileBytes int64
}

func NewStorage(uploadsDir string, maxFileBytes int64) *Storage {
	return &Storage{
		rootDir:      filepath.Dir(uploadsDir),
		uploadsDir:   uploadsDir,
		maxFileBytes: maxFileBytes,
	}
}

func (s *Storage) RootDir() string {
	return s.rootDir
}

func (s *Storage) MaxFileBytes() int64 {
	return s.maxFileBytes
}

func (s *Storage) Save(name, mimeType string, r io.Reader, size int64) (SavedFile, error) {
	if size > s.maxFileBytes {
		return SavedFile{}, ErrTooLarge
	}
	safeName := sanitizeName(name)
	if err := os.MkdirAll(s.uploadsDir, 0o755); err != nil {
		return SavedFile{}, err
	}

	var relativePath string
	var storedName string
	var file *os.File
	for range 10 {
		prefix, err := randomHex(12)
		if err != nil {
			return SavedFile{}, err
		}
		storedName = prefix + "_" + safeName
		relativePath = "uploads/" + storedName
		fullPath := filepath.Join(s.uploadsDir, storedName)
		file, err = os.OpenFile(fullPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return SavedFile{}, err
		}
		break
	}
	if file == nil {
		return SavedFile{}, errors.New("could not create unique upload path")
	}

	written, err := copyWithLimit(file, r, s.maxFileBytes)
	closeErr := file.Close()
	if err != nil {
		_ = os.Remove(filepath.Join(s.uploadsDir, storedName))
		return SavedFile{}, err
	}
	if closeErr != nil {
		_ = os.Remove(filepath.Join(s.uploadsDir, storedName))
		return SavedFile{}, closeErr
	}
	return SavedFile{
		OriginalName: safeName,
		MimeType:     mimeType,
		Size:         written,
		RelativePath: relativePath,
	}, nil
}

func (s *Storage) Open(relativePath string) (io.ReadCloser, error) {
	fullPath, err := s.safePath(relativePath)
	if err != nil {
		return nil, err
	}
	return os.Open(fullPath)
}

func (s *Storage) Delete(relativePath string) error {
	fullPath, err := s.safePath(relativePath)
	if err != nil {
		return err
	}
	err = os.Remove(fullPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (s *Storage) safePath(relativePath string) (string, error) {
	if filepath.IsAbs(relativePath) || strings.Contains(relativePath, `\`) {
		return "", errors.New("invalid upload path")
	}
	clean := path.Clean(relativePath)
	if clean != relativePath || clean == "." {
		return "", errors.New("invalid upload path")
	}
	parts := strings.Split(clean, "/")
	if len(parts) < 2 || parts[0] != "uploads" {
		return "", errors.New("invalid upload path")
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", errors.New("invalid upload path")
		}
	}
	return filepath.Join(s.rootDir, filepath.FromSlash(clean)), nil
}

func copyWithLimit(dst io.Writer, src io.Reader, max int64) (int64, error) {
	var written int64
	buf := make([]byte, 32*1024)
	for {
		n, readErr := src.Read(buf)
		if n > 0 {
			if written+int64(n) > max {
				allowed := max - written
				if allowed > 0 {
					nw, err := dst.Write(buf[:allowed])
					written += int64(nw)
					if err != nil {
						return written, err
					}
					if int64(nw) != allowed {
						return written, io.ErrShortWrite
					}
				}
				return written, ErrTooLarge
			}
			nw, writeErr := dst.Write(buf[:n])
			written += int64(nw)
			if writeErr != nil {
				return written, writeErr
			}
			if nw != n {
				return written, io.ErrShortWrite
			}
		}
		if readErr == io.EOF {
			return written, nil
		}
		if readErr != nil {
			return written, readErr
		}
	}
}

func sanitizeName(name string) string {
	parts := strings.FieldsFunc(name, func(r rune) bool {
		return r == '/' || r == '\\'
	})
	cleanParts := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.Trim(part, ".")
		if part == "" || part == "." || part == ".." {
			continue
		}
		cleanParts = append(cleanParts, sanitizePart(part))
	}
	safe := strings.Join(cleanParts, "_")
	safe = strings.Trim(safe, "._-")
	if safe == "" {
		return "file"
	}
	return safe
}

func sanitizePart(part string) string {
	var b strings.Builder
	lastUnderscore := false
	for _, r := range part {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-' || r == '_' {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	return b.String()
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
