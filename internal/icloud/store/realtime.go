package store

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// PublishRealtimeChange 发布不属于持久化实体的运行时变更。
// 复刻自原 sqlite 实现。
func (s *Store) PublishRealtimeChange(resource, resourceID, operation string, payload any) error {
	resource = strings.TrimSpace(resource)
	if resource == "" {
		return errors.New("实时变更资源不能为空")
	}
	if operation = strings.TrimSpace(operation); operation == "" {
		operation = "updated"
	}
	payloadData, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if payload == nil {
		payloadData = []byte(`{}`)
	}
	change := Change{Type: resource + "." + operation, Resource: resource, ResourceID: strings.TrimSpace(resourceID), Operation: operation, Payload: payloadData, CreatedAt: time.Now()}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.commitChanges([]Change{change})
}

// SaveRuntimeState 持久化后台运行状态，并按需发布一条实时变更。
// 复刻自原 sqlite 实现。
func (s *Store) SaveRuntimeState(id string, value any, publish bool) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("运行状态标识不能为空")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	change, changed, err := s.upsertEntity("runtime_states", id, id, value)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	if !publish {
		change = Change{}
	}
	changes := []Change{}
	if change.Resource != "" {
		changes = append(changes, change)
	}
	return s.commitChanges(changes)
}

// LoadRuntimeState 复刻自原 sqlite 实现。
func (s *Store) LoadRuntimeState(id string, target any) (bool, error) {
	return s.readEntity("runtime_states", strings.TrimSpace(id), target)
}
