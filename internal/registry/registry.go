package registry

import (
	"sync"
	"time"
)

// Worker represents a connected swarm-agent.
type Worker struct {
	ID             string
	Hostname       string
	Models         []string
	Capabilities   []string
	Labels         []string
	Busy           bool
	CurrentSession string
	LastSeen       time.Time
	Send           chan []byte // buffered channel for outbound messages
}

// Registry holds all registered workers.
type Registry struct {
	mu      sync.RWMutex
	workers map[string]*Worker
}

// New returns an empty Registry.
func New() *Registry {
	return &Registry{workers: make(map[string]*Worker)}
}

// Register adds or replaces a worker.
func (r *Registry) Register(w *Worker) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.workers[w.ID] = w
}

// Unregister removes a worker.
func (r *Registry) Unregister(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.workers, id)
}

// Get returns the worker with the given ID.
func (r *Registry) Get(id string) (*Worker, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	w, ok := r.workers[id]
	return w, ok
}

// All returns a snapshot of all workers.
func (r *Registry) All() []*Worker {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Worker, 0, len(r.workers))
	for _, w := range r.workers {
		out = append(out, w)
	}
	return out
}

// SetBusy marks a worker as busy or free.
func (r *Registry) SetBusy(id string, busy bool, session string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	w, ok := r.workers[id]
	if !ok {
		return
	}
	w.Busy = busy
	if busy {
		w.CurrentSession = session
	} else {
		w.CurrentSession = ""
	}
}

// UpdateHeartbeat records the current time as the worker's last seen time.
func (r *Registry) UpdateHeartbeat(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if w, ok := r.workers[id]; ok {
		w.LastSeen = time.Now()
	}
}
