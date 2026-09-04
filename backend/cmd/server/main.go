// Command server 启动 HTTP API 服务。
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hibiken/asynq"

	"github.com/eddiel/fallsurvivor/backend/config"
	"github.com/eddiel/fallsurvivor/backend/internal/repository"
	"github.com/eddiel/fallsurvivor/backend/internal/router"
	"github.com/eddiel/fallsurvivor/backend/pkg/logger"
)

func main() {
	if err := run(); err != nil {
		// 启动失败信息不含任何密钥。
		fmt.Fprintf(os.Stderr, "启动失败: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger.Init(cfg.LogLevel, cfg.IsProduction())
	slog.Info("正在启动 秋招 OS API 服务",
		"env", cfg.Env,
		"port", cfg.ServerPort,
		"llm_enabled", cfg.LLM.Enabled(),
		"search_enabled", cfg.Tavily.Enabled())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := repository.Open(ctx, cfg.DatabaseURL, !cfg.IsProduction())
	if err != nil {
		return err
	}
	store := repository.NewStore(db)

	redisOpt, err := asynq.ParseRedisURI(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("解析 REDIS_URL 失败: %w", err)
	}
	enqueuer := asynq.NewClient(redisOpt)
	defer enqueuer.Close()

	svcs, err := router.BuildServices(cfg, store, enqueuer)
	if err != nil {
		return err
	}

	// 清理上次进程遗留的僵死任务。
	if n, err := store.SearchTask.ReclaimStale(ctx, 30*time.Minute); err != nil {
		slog.Warn("回收僵死搜索任务失败", "error", err.Error())
	} else if n > 0 {
		slog.Info("已回收僵死搜索任务", "count", n)
	}

	// 兜底：把任何仍停留在 PENDING/RUNNING 的残留任务（如 worker 异常退出未清理）标记 TERMINATED。
	if n, err := store.SearchTask.ReclaimToTerminated(ctx); err != nil {
		slog.Warn("终止残留搜索任务失败", "error", err.Error())
	} else if n > 0 {
		slog.Info("已终止残留搜索任务", "count", n)
	}

	engine := router.New(cfg, store, svcs)

	srv := &http.Server{
		// 只监听回环地址：这是个人本地工具，不应暴露到局域网。
		Addr:              fmt.Sprintf("127.0.0.1:%d", cfg.ServerPort),
		Handler:           engine,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		// 探索新招聘站点会经过浏览器操作、网络观测、LLM 生成与验证；
		// handler 自身用 5 分钟 context 控制上限，HTTP 写超时需要覆盖它。
		WriteTimeout:   6 * time.Minute,
		IdleTimeout:    120 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}
	// 容器内需要监听所有网卡才能被 compose 网络访问。
	if os.Getenv("BIND_ALL") == "true" {
		srv.Addr = fmt.Sprintf(":%d", cfg.ServerPort)
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("HTTP 服务已就绪", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		slog.Info("收到退出信号，开始优雅关闭")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	slog.Info("服务已关闭")
	return nil
}
