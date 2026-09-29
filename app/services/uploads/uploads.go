// Package uploads tracks in-progress blob uploads. Each session is a temp
// file under Root; sessions live in memory only, so a restart discards them
// and clients simply retry.
package uploads

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// ErrNotFound marks lookups of unknown or expired upload sessions.
var ErrNotFound = errors.New("upload session unknown")

// Session is one in-progress upload.
type Session struct {
	UUID   string
	Repo   string
	Path   string
	Offset int64
}

// Manager keeps the live upload sessions of this process.
type Manager struct {
	root     string
	mu       sync.Mutex
	sessions map[string]*Session
}

func NewManager(root string) *Manager {
	return &Manager{root: root, sessions: map[string]*Session{}}
}

func newUUID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// Start opens a fresh empty session for repo.
func (m *Manager) Start(repo string) (*Session, error) {
	uuid, err := newUUID()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(m.root, 0o755); err != nil {
		return nil, err
	}

	path := filepath.Join(m.root, uuid+".part")
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return nil, err
	}

	sess := &Session{UUID: uuid, Repo: repo, Path: path}
	m.mu.Lock()
	m.sessions[uuid] = sess
	m.mu.Unlock()
	return sess, nil
}

// Get returns the session for uuid.
func (m *Manager) Get(uuid string) (*Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[uuid]
	return sess, ok
}

// Append streams data into the session's temp file and advances its
// offset. Writes are serialized per manager.
func (m *Manager) Append(uuid string, r io.Reader) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sess, ok := m.sessions[uuid]
	if !ok {
		return 0, ErrNotFound
	}

	f, err := os.OpenFile(sess.Path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return 0, err
	}
	written, err := io.Copy(f, r)
	closeErr := f.Close()
	if err != nil {
		return written, err
	}
	if closeErr != nil {
		return written, closeErr
	}

	sess.Offset += written
	return written, nil
}

// Remove drops the session and deletes its temp file (complete or abort).
func (m *Manager) Remove(uuid string) {
	m.mu.Lock()
	sess, ok := m.sessions[uuid]
	if ok {
		delete(m.sessions, uuid)
	}
	m.mu.Unlock()

	if ok {
		_ = os.Remove(sess.Path)
	}
}
