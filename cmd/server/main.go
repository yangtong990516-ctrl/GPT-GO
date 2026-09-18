// Command server starts the GPT-GO local control service, mirroring reference
// app/backend/__main__.py (uvicorn on 127.0.0.1:8000).
package main

import (
	"context"
	"flag"
	"net/http"
	"strconv"
	"time"

	"go.uber.org/zap"

	"gpt-go/internal/apiserver"
	"gpt-go/internal/config"
	"gpt-go/internal/store/mongo"
	"gpt-go/internal/util"
)

func main() {
	configPath := flag.String("config", "config/config.yaml", "path to YAML config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		util.InitLogger("info", false)
		util.Logger().Fatal("load config", zap.Error(err))
	}

	// Unified logger (CONTRACT §5) driven by config.yaml log.level / log.json.
	util.InitLogger(cfg.Log.Level, cfg.Log.JSON)
	log := util.Logger()

	mongoManager := mongo.NewManager(cfg.MongoDB.Database)
	// 建立真实 Mongo 连接(主站业务 store 持久化 + /api/health 真实状态)。
	// 连接失败不致命:NewServer 会回退内存 store 并记录警告。
	mongoClient, err := mongo.Connect(context.Background(), cfg.MongoDB.URI, mongoManager)
	if err != nil {
		log.Warn("mongo connect failed, 主站业务 store 将回退内存存储(重启即失)",
			zap.String("uri", cfg.MongoDB.URI), zap.Error(err))
		mongoClient = nil
	} else {
		// 后台探活,让 /api/health 的 mongo 状态反映真实连接(断线/恢复)。
		mongo.WatchHealth(context.Background(), mongoClient, mongoManager, 5*time.Second)
	}
	server := apiserver.NewServer(cfg, mongoManager, mongoClient)

	// Start the sentinel version-check loop (honors the enabled flag), then
	// ensure it stops on shutdown.
	server.StartBackground()
	defer server.StopBackground()

	addr := cfg.Server.Host + ":" + strconv.Itoa(cfg.Server.Port)
	log.Info("service started",
		zap.String("title", cfg.App.Title),
		zap.String("addr", addr),
	)
	if err := http.ListenAndServe(addr, server.Handler()); err != nil {
		log.Fatal("server error", zap.Error(err))
	}
}
