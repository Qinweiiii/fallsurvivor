// Command worker 启动异步任务消费者。
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/hibiken/asynq"

	"github.com/eddiel/fallsurvivor/backend/config"
	"github.com/eddiel/fallsurvivor/backend/internal/repository"
	"github.com/eddiel/fallsurvivor/backend/internal/router"
	"github.com/eddiel/fallsurvivor/backend/internal/task"
	"github.com/eddiel/fallsurvivor/backend/pkg/logger"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Worker 启动失败: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger.Init(cfg.LogLevel, cfg.IsProduction())
	slog.Info("正在启动异步任务消费者")

	ctx := context.Background()

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

	// 启动清理：把上次进程遗留的（崩溃未结束的）搜索任务统一标记为 TERMINATED。
	if n, err := store.SearchTask.ReclaimToTerminated(ctx); err != nil {
		slog.Warn("回收残留搜索任务失败", "error", err.Error())
	} else if n > 0 {
		slog.Info("已终止上次遗留的搜索任务", "count", n)
	}

	srv := asynq.NewServer(redisOpt, asynq.Config{
		// 搜索任务内部已有并发控制，此处保持较低并发即可。
		Concurrency: 2,
		Queues:      map[string]int{task.QueueDefault: 1},
		Logger:      &asynqLogger{},
	})

	mux := asynq.NewServeMux()
	task.NewHandler(svcs.Pipeline).Register(mux)

	// 支持优雅退出。
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		<-sigCh
		slog.Info("收到退出信号，正在停止消费者并终止进行中的搜索任务")
		// 先 Shutdown：asynq 会取消正在执行任务的 ctx，pipeline 据此将 RUNNING 标记 TERMINATED。
		srv.Shutdown()
		// 再兜底：把队列中尚未被消费的 PENDING，以及任何竞态窗口里残留的 RUNNING 标记为 TERMINATED。
		if n, err := store.SearchTask.ReclaimToTerminated(ctx); err != nil {
			slog.Warn("终止残留搜索任务失败", "error", err.Error())
		} else if n > 0 {
			slog.Info("已终止残留搜索任务", "count", n)
		}
	}()

	slog.Info("消费者已就绪")
	return srv.Run(mux)
}

// asynqLogger 把 asynq 日志桥接到 slog。
type asynqLogger struct{}

func (l *asynqLogger) Debug(args ...any) { slog.Debug(fmt.Sprint(args...)) }
func (l *asynqLogger) Info(args ...any)  { slog.Info(fmt.Sprint(args...)) }
func (l *asynqLogger) Warn(args ...any)  { slog.Warn(fmt.Sprint(args...)) }
func (l *asynqLogger) Error(args ...any) { slog.Error(fmt.Sprint(args...)) }
func (l *asynqLogger) Fatal(args ...any) { slog.Error(fmt.Sprint(args...)) }
