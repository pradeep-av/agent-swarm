package session

import (
	"sync"
	"time"
)

// Session maps a master session (from the coordinator) to a specific worker.
type Session struct {
	MasterID   string
	WorkerID   string
	LastAccess time.Time
}

// Store is an in-memory session store.
type Store struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

// NewStore returns an empty Store.
func NewStore() *Store {
	return &Store{sessions: make(map[string]*Session)}
}

// Get returns the session for the given master session ID.
func (s *Store) Get(masterID string) (*Session, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.sessions[masterID]
	return sess, ok
}

// Save persists or updates a session.
func (s *Store) Save(sess *Session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess.LastAccess = time.Now()
	s.sessions[sess.MasterID] = sess
}

// Touch updates the last-access timestamp for an existing session.
func (s *Store) Touch(masterID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess, ok := s.sessions[masterID]; ok {
		sess.LastAccess = time.Now()
	}
}
