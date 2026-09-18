package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-go/internal/icloud/store"
)

// handleRealtime 复刻自原 net/http 实现的 SSE 实时事件流：
// 断线续传（Last-Event-ID / ?after=）、replayRealtimeChanges、15s 心跳、retry:2000 首帧。
// gin 版用 c.Writer.Header() 设置 SSE 头、c.Writer.(http.Flusher) 取 Flusher、
// c.Request.Context().Done() 检测断开。
func (s *Server) handleRealtime(c *gin.Context) {
	w := c.Writer
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(c, http.StatusInternalServerError, "realtime_unsupported", "当前连接不支持实时事件流")
		return
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	channel, unsubscribe := s.store.SubscribeChanges(128)
	defer unsubscribe()

	lastSequence, replay := realtimeSequence(c)
	if !replay {
		var err error
		lastSequence, err = s.store.LatestChangeSequence()
		if err != nil {
			writeError(c, http.StatusInternalServerError, "realtime_sequence_failed", "读取实时变更序号失败")
			return
		}
	}

	if _, err := fmt.Fprint(w, "retry: 2000\n\n"); err != nil {
		return
	}
	flusher.Flush()

	if replay {
		if err := s.replayRealtimeChanges(w, flusher, &lastSequence); err != nil {
			s.log.Warn("回放实时变更记录失败", "错误", err)
			return
		}
	}

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case <-heartbeat.C:
			if err := s.replayRealtimeChanges(w, flusher, &lastSequence); err != nil {
				s.log.Warn("补齐实时变更记录失败", "错误", err)
				return
			}
			if _, err := fmt.Fprintf(w, ": 心跳 %d\n\n", time.Now().Unix()); err != nil {
				return
			}
			flusher.Flush()
		case change, open := <-channel:
			if !open {
				return
			}
			if change.Sequence <= lastSequence {
				continue
			}
			if change.Sequence > lastSequence+1 {
				if err := s.replayRealtimeChanges(w, flusher, &lastSequence); err != nil {
					s.log.Warn("补齐实时变更记录失败", "错误", err)
					return
				}
				if change.Sequence <= lastSequence {
					continue
				}
			}
			if err := writeRealtimeChange(w, flusher, change); err != nil {
				return
			}
			lastSequence = change.Sequence
		}
	}
}

// realtimeSequence 复刻自原 net/http 实现（读取 Last-Event-ID / ?after=）。
func realtimeSequence(c *gin.Context) (int64, bool) {
	value := strings.TrimSpace(c.GetHeader("Last-Event-ID"))
	if value == "" {
		if values, ok := c.Request.URL.Query()["after"]; ok && len(values) > 0 {
			value = strings.TrimSpace(values[0])
		} else {
			return 0, false
		}
	}
	sequence, _ := strconv.ParseInt(value, 10, 64)
	if sequence < 0 {
		sequence = 0
	}
	return sequence, true
}

// replayRealtimeChanges 复刻自原 net/http 实现。
func (s *Server) replayRealtimeChanges(w http.ResponseWriter, flusher http.Flusher, lastSequence *int64) error {
	for {
		changes, err := s.store.ChangesAfter(*lastSequence, 500)
		if err != nil {
			return err
		}
		for _, change := range changes {
			if err := writeRealtimeChange(w, flusher, change); err != nil {
				return err
			}
			*lastSequence = change.Sequence
		}
		if len(changes) < 500 {
			return nil
		}
	}
}

// writeRealtimeChange 复刻自原 net/http 实现。
func writeRealtimeChange(w http.ResponseWriter, flusher http.Flusher, change store.Change) error {
	data, err := json.Marshal(change)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "id: %d\nevent: change\ndata: %s\n\n", change.Sequence, data); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}
