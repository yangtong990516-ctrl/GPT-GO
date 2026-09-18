package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// backupRetentionCount 备份保留份数，复刻自原 cfg.DatabaseBackupRetentionCount
// 的默认值（原配置字段已删除，这里固定为 3）。
const backupRetentionCount = 3

// backupDir 返回备份目录。原 cfg.DatabaseBackupDir 字段已删除，
// 按迁移要求固定为 data/icloud-backups。
func backupDir() string {
	return filepath.Join("data", "icloud-backups")
}

// handleDatabaseStatus 复刻自原 net/http 实现（backup_dir/retention 改用 backupDir/常量）。
func (s *Server) handleDatabaseStatus(c *gin.Context) {
	writeJSON(c, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"database":               s.store.DatabaseStatus(),
		"backup_dir":             backupDir(),
		"backup_retention_count": backupRetentionCount,
		"message_retention_days": s.cfg.DatabaseMessageRetentionDays,
	}})
}

// handleDatabaseBackup 复刻自原 net/http 实现。
func (s *Server) handleDatabaseBackup(c *gin.Context) {
	path, err := s.createDatabaseBackup()
	if err != nil {
		s.cleanupDatabaseBackups()
		_ = s.store.RecordMaintenance("backup", "failed", err.Error())
		writeError(c, http.StatusInternalServerError, "database_backup_failed", "创建数据库备份失败："+err.Error())
		return
	}
	s.cleanupDatabaseBackups()
	_ = s.store.RecordMaintenance("backup", "success", path)
	writeJSON(c, http.StatusOK, map[string]any{"success": true, "data": map[string]any{"path": path}})
}

// handleDatabaseCheck 复刻自原 net/http 实现。
func (s *Server) handleDatabaseCheck(c *gin.Context) {
	result, err := s.store.IntegrityCheck()
	if err != nil {
		_ = s.store.RecordMaintenance("integrity-check", "failed", err.Error())
		writeError(c, http.StatusInternalServerError, "database_check_failed", "数据库完整性检查失败："+err.Error())
		return
	}
	status := "success"
	if !strings.EqualFold(strings.TrimSpace(result), "ok") {
		status = "failed"
	}
	_ = s.store.RecordMaintenance("integrity-check", status, result)
	writeJSON(c, http.StatusOK, map[string]any{"success": status == "success", "data": map[string]any{"result": result}})
}

// handleDatabaseOptimize 复刻自原 net/http 实现。
func (s *Server) handleDatabaseOptimize(c *gin.Context) {
	if err := s.store.Checkpoint(); err != nil {
		writeError(c, http.StatusInternalServerError, "database_checkpoint_failed", "合并 WAL 失败："+err.Error())
		return
	}
	if err := s.store.Vacuum(); err != nil {
		_ = s.store.RecordMaintenance("optimize", "failed", err.Error())
		writeError(c, http.StatusInternalServerError, "database_optimize_failed", "回收数据库空间失败："+err.Error())
		return
	}
	_ = s.store.RecordMaintenance("optimize", "success", "已完成 WAL checkpoint 和 VACUUM")
	writeJSON(c, http.StatusOK, map[string]any{"success": true, "data": map[string]any{"database": s.store.DatabaseStatus()}})
}

// runDatabaseMaintenance 复刻自原 net/http 实现。
func (s *Server) runDatabaseMaintenance(ctx context.Context) {
	// 启动一分钟后执行首轮，之后每 24 小时执行一次。
	timer := time.NewTimer(time.Minute)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			s.performDatabaseMaintenance()
			timer.Reset(24 * time.Hour)
		}
	}
}

// performDatabaseMaintenance 复刻自原 net/http 实现（保留份数改用常量）。
func (s *Server) performDatabaseMaintenance() {
	result, err := s.store.IntegrityCheck()
	if err != nil || !strings.EqualFold(strings.TrimSpace(result), "ok") {
		detail := result
		if err != nil {
			detail = err.Error()
		}
		_ = s.store.RecordMaintenance("automatic", "failed", detail)
		s.log.Error("数据库自动完整性检查异常", "详情", detail)
		return
	}
	deleted, err := s.store.PruneMessagesBefore(time.Now().AddDate(0, 0, -s.cfg.DatabaseMessageRetentionDays))
	if err != nil {
		// 清理失败也落 maintenance_runs(原本只 log,运维看不到失败历史)。
		_ = s.store.RecordMaintenance("automatic", "failed", "清理过期邮件失败: "+err.Error())
		s.log.Warn("清理过期邮件失败", "错误", err)
	}
	backupPath, backupErr := s.createDatabaseBackup()
	// Mongo 迁移后 Checkpoint/Vacuum 已是 no-op(store/maintenance.go),不再记录误导性日志。
	if backupErr != nil {
		s.cleanupDatabaseBackups()
		_ = s.store.RecordMaintenance("automatic", "failed", backupErr.Error())
		s.log.Warn("自动数据库备份失败", "错误", backupErr)
		return
	}
	s.cleanupDatabaseBackups()
	detail := fmt.Sprintf("备份=%s，清理过期邮件=%d，最多保留=%d份", backupPath, deleted, backupRetentionCount)
	_ = s.store.RecordMaintenance("automatic", "success", detail)
	s.log.Info("数据库自动维护完成", "备份", backupPath, "清理过期邮件", deleted, "最多保留", backupRetentionCount)
}

// createDatabaseBackup 复刻自原 net/http 实现（目录改用 backupDir()）。
func (s *Server) createDatabaseBackup() (string, error) {
	dir := filepath.Clean(backupDir())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "app-"+time.Now().Format("20060102-150405.000")+".db")
	return path, s.store.BackupDatabase(path)
}

// cleanupDatabaseBackups 清理过期备份。
// Mongo 迁移后,BackupDatabase 的产物是 `app-时间戳.db/`【目录】(内含每集合 JSON),
// 不再是单文件 + .key。因此这里识别该目录形态,超龄/超份用 RemoveAll 递归删除,
// 防止备份目录无限积累(磁盘泄漏)。
func (s *Server) cleanupDatabaseBackups() {
	dir := filepath.Clean(backupDir())
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type backupItem struct {
		name    string
		modTime time.Time
	}
	backups := make([]backupItem, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		// 只认 app-*.db 命名的备份目录(Mongo 备份是目录);其余文件/目录不碰。
		if !entry.IsDir() || !strings.HasPrefix(name, "app-") || !strings.HasSuffix(name, ".db") {
			continue
		}
		info, err := entry.Info()
		if err == nil {
			backups = append(backups, backupItem{name: name, modTime: info.ModTime()})
		}
	}
	sort.Slice(backups, func(i, j int) bool {
		if backups[i].modTime.Equal(backups[j].modTime) {
			return backups[i].name > backups[j].name
		}
		return backups[i].modTime.After(backups[j].modTime)
	})
	retentionCount := backupRetentionCount
	if retentionCount <= 0 {
		retentionCount = 3
	}
	if len(backups) <= retentionCount {
		return
	}
	for _, backup := range backups[retentionCount:] {
		_ = os.RemoveAll(filepath.Join(dir, backup.name))
	}
}
