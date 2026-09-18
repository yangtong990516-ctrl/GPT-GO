// client.go 主站 Mongo 连接基座:建立真实连接、接入 Manager 健康上报、
// 提供各业务 store 共享的轻量 helper。参照 internal/icloud/store 的连接方式,
// 但用 mongo-driver 原生 bson 编解码(主站 store 是强类型 struct,无需 json 中间表示)。
package mongo

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
)

// connectTimeout 是首次连接+ping 的超时。
const connectTimeout = 10 * time.Second

// Connect 建立到 Mongo 的连接并 ping 确认,成功则把 Manager 标记 online,
// 失败标记 offline(带错误)。返回的 client 供各业务 store 使用。
// 调用方决定失败时是否回退内存 store(见 server 装配)。
func Connect(ctx context.Context, uri string, m *Manager) (*mongo.Client, error) {
	cctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	client, err := mongo.Connect(cctx, options.Client().ApplyURI(uri))
	if err != nil {
		if m != nil {
			m.MarkOffline(err)
		}
		return nil, err
	}
	if err := client.Ping(cctx, readpref.Primary()); err != nil {
		if m != nil {
			m.MarkOffline(err)
		}
		_ = client.Disconnect(context.Background())
		return nil, err
	}
	if m != nil {
		m.MarkOnline()
	}
	return client, nil
}

// WatchHealth 启动一个后台探活协程:周期性 ping,把连接状态同步到 Manager,
// 使 /api/health 的 mongo 状态反映真实连接(断线→reconnecting,恢复→online)。
// ctx 取消时退出。这是轻量实现(不自动重连驱动,mongo-driver 自身已维护连接池)。
func WatchHealth(ctx context.Context, client *mongo.Client, m *Manager, interval time.Duration) {
	if client == nil || m == nil {
		return
	}
	if interval <= 0 {
		interval = 5 * time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
				err := client.Ping(pctx, readpref.Primary())
				cancel()
				if err != nil {
					m.MarkOffline(err)
				} else {
					m.MarkOnline()
				}
			}
		}
	}()
}
