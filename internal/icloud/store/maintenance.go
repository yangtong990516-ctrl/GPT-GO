package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// DatabaseStatus 复刻自原 sqlite 实现：
// JournalMode/WALBytes 为 SQLite 专有，MongoDB 版填占位值；
// DatabaseBytes 改用 dbStats 命令的 storageSize。
func (s *Store) DatabaseStatus() DatabaseStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	status := DatabaseStatus{Path: s.Path(), SchemaVersion: databaseSchemaVersion, JournalMode: "mongodb", EncryptedFields: s.codec != nil}
	ctx, cancel := s.ctx()
	defer cancel()
	var stats bson.M
	if err := s.db.RunCommand(ctx, bson.D{{Key: "dbStats", Value: 1}, {Key: "scale", Value: 1}}).Decode(&stats); err == nil {
		status.DatabaseBytes = int64Value(stats["storageSize"])
	}
	status.ChangeLogCount = s.countDocs("change_log", bson.M{})
	latest, _ := s.LatestChangeSequence()
	status.LatestSequence = latest
	// 复刻原最近一次成功维护记录查询。
	var run bson.M
	if err := s.col("maintenance_runs").FindOne(ctx, bson.M{"status": "success"},
		options.FindOne().SetSort(bson.D{{Key: "_id", Value: -1}})).Decode(&run); err == nil {
		status.LastMaintenance = stringValue(run["operation"])
		status.LastMaintenanceAt = stringValue(run["created_at"])
	}
	return status
}

// RecordMaintenance 复刻自原 sqlite 实现。
func (s *Store) RecordMaintenance(operation, status, detail string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, cancel := s.ctx()
	defer cancel()
	_, err := s.col("maintenance_runs").InsertOne(ctx, bson.M{
		"operation":  strings.TrimSpace(operation),
		"status":     strings.TrimSpace(status),
		"detail":     strings.TrimSpace(detail),
		"created_at": time.Now().Format(time.RFC3339Nano),
	})
	return err
}

// IntegrityCheck 复刻自原 sqlite 实现：MongoDB 版改为 ping，成功返回 "ok"。
func (s *Store) IntegrityCheck() (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.client.Ping(ctx, nil); err != nil {
		return "", err
	}
	return "ok", nil
}

// Checkpoint 复刻自原 sqlite 实现的语义占位：MongoDB 无 WAL 概念，no-op。
func (s *Store) Checkpoint() error {
	return nil
}

// Vacuum 复刻自原 sqlite 实现的语义占位：MongoDB 无需手动页回收，no-op。
func (s *Store) Vacuum() error {
	return nil
}

// BackupDatabase 复刻自原 sqlite 实现的对外语义（导出一致性快照到 destination），
// MongoDB 版改为：把各集合导出为 destination 指定目录下的 JSON 文件（每集合一个数组），0600 权限。
func (s *Store) BackupDatabase(destination string) error {
	destination = filepath.Clean(strings.TrimSpace(destination))
	if destination == "." || destination == "" {
		return errors.New("备份路径不能为空")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	if info, err := os.Stat(destination); err == nil && !info.IsDir() {
		return errors.New("备份文件已经存在")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return err
	}
	// 导出全部实体集合（含 runtime_states），每集合一个 JSON 数组文件。
	collections := append(append([]string(nil), entityCollections...), "runtime_states")
	for _, collection := range collections {
		items := make([]json.RawMessage, 0)
		appendFn := func(data []byte) error {
			// 备份导出明文（与原 VACUUM INTO + .key 分离的语义不同，
			// 这里导出解密后的数据并以 0600 权限保护，便于恢复）。
			plain, err := s.unprotectJSON(collection, data)
			if err != nil {
				return err
			}
			items = append(items, plain)
			return nil
		}
		if err := s.findDocs(collection, bson.M{}, nil, appendFn); err != nil {
			return fmt.Errorf("导出集合 %s 失败：%w", collection, err)
		}
		out, err := json.MarshalIndent(items, "", "  ")
		if err != nil {
			return err
		}
		path := filepath.Join(destination, collection+".json")
		if err := os.WriteFile(path, out, 0o600); err != nil {
			return err
		}
	}
	return nil
}
