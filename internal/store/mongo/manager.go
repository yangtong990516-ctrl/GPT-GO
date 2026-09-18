// Package mongo provides MongoDB connection management and health reporting,
// mirroring reference app/backend/mongo_manager.py.
package mongo

import (
	"sync"

	"gpt-go/internal/model"
)

// Mongo status values mirror MongoHealth.status.
const (
	StatusOnline       = "online"
	StatusOffline      = "offline"
	StatusReconnecting = "reconnecting"
)

// Manager holds the live connection state observable by the health endpoint:
// online / reconnecting / error / nextRetrySeconds. Full connection lifecycle
// (start/stop/probe/monitor) is added in a later task; this state model already
// matches mongo_manager.health().
type Manager struct {
	mu sync.RWMutex

	database     string
	online       bool
	reconnecting bool
	err          *string
	nextRetry    *int
}

// NewManager returns a Manager for the given database name.
func NewManager(database string) *Manager {
	return &Manager{database: database}
}

// Health reports current health, matching mongo_manager.health().
func (m *Manager) Health() model.MongoHealth {
	m.mu.RLock()
	defer m.mu.RUnlock()

	status := StatusOffline
	switch {
	case m.online:
		status = StatusOnline
	case m.reconnecting:
		status = StatusReconnecting
	}

	return model.MongoHealth{
		Status:           status,
		Database:         m.database,
		Error:            m.err,
		NextRetrySeconds: m.nextRetry,
	}
}

// Status returns the raw status string ("online"|"offline"|"reconnecting").
func (m *Manager) Status() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	switch {
	case m.online:
		return StatusOnline
	case m.reconnecting:
		return StatusReconnecting
	default:
		return StatusOffline
	}
}

// MarkOnline sets the manager online.
func (m *Manager) MarkOnline() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.online = true
	m.reconnecting = false
	m.err = nil
	m.nextRetry = nil
}

// MarkOffline sets the manager offline and records an error message.
func (m *Manager) MarkOffline(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.online = false
	m.reconnecting = true
	msg := err.Error()
	m.err = &msg
}

// MarkReconnecting sets the reconnecting state with an optional next-retry delay
// (seconds), mirroring nextRetrySeconds.
func (m *Manager) MarkReconnecting(err error, nextRetrySeconds *int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.online = false
	m.reconnecting = true
	if err != nil {
		msg := err.Error()
		m.err = &msg
	}
	m.nextRetry = nextRetrySeconds
}
