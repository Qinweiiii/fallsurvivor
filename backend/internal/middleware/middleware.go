// Package middleware 提供 HTTP 中间件。
package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/eddiel/fallsurvivor/backend/internal/security"
	"github.com/eddiel/fallsurvivor/backend/pkg/response"
)

// RequestID 为每个请求分配唯一 ID，便于日志追踪。
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader("X-Request-ID")
		if id == "" {
			id = uuid.NewString()
		}
		c.Set("request_id", id)
		c.Header("X-Request-ID", id)
		c.Next()
	}
}

// Logger 记录访问日志。日志内容经过脱敏，不含查询串中的敏感值。
func Logger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		c.Next()

		attrs := []any{
			"method", c.Request.Method,
			"path", path,
			"status", c.Writer.Status(),
			"duration_ms", time.Since(start).Milliseconds(),
			"request_id", c.GetString("request_id"),
		}
		if len(c.Errors) > 0 {
			// 错误详情只进服务端日志，且先脱敏。
			attrs = append(attrs, "error", security.RedactPII(c.Errors.String()))
			slog.Error("请求处理出错", attrs...)
			return
		}
		if c.Writer.Status() >= 500 {
			slog.Error("请求返回服务端错误", attrs...)
			return
		}
		slog.Info("请求完成", attrs...)
	}
}

// Recovery 捕获 panic，避免进程退出，并且不向客户端泄漏堆栈。
func Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("捕获到 panic",
					"error", r,
					"stack", string(debug.Stack()),
					"path", c.Request.URL.Path,
					"request_id", c.GetString("request_id"))
				response.Internal(c)
			}
		}()
		c.Next()
	}
}

// SecurityHeaders 设置基础安全响应头。
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		// API 只返回 JSON，禁止任何内嵌资源加载。
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		h.Set("Cache-Control", "no-store")
		c.Next()
	}
}

// BodyLimit 限制请求体大小，防止内存耗尽。
func BodyLimit(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.ContentLength > maxBytes {
			response.Fail(c, http.StatusRequestEntityTooLarge,
				response.CodePayloadTooLarge, "请求体过大")
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		c.Next()
	}
}

// rateLimiter 是简单的固定窗口限流器。
//
// 本项目为单用户本地工具，限流的目的是防止界面误操作或脚本失控
// 导致对上游 API（DeepSeek / Tavily）的过量调用，而非防御分布式攻击。
type rateLimiter struct {
	mu       sync.Mutex
	counters map[string]*counter
	limit    int
	window   time.Duration
}

type counter struct {
	count     int
	windowEnd time.Time
}

// RateLimit 返回按 IP 限流的中间件。
func RateLimit(limit int, window time.Duration) gin.HandlerFunc {
	rl := &rateLimiter{
		counters: make(map[string]*counter),
		limit:    limit,
		window:   window,
	}

	// 定期清理过期计数，避免内存无限增长。
	go func() {
		ticker := time.NewTicker(window * 2)
		defer ticker.Stop()
		for range ticker.C {
			now := time.Now()
			rl.mu.Lock()
			for k, v := range rl.counters {
				if now.After(v.windowEnd) {
					delete(rl.counters, k)
				}
			}
			rl.mu.Unlock()
		}
	}()

	return func(c *gin.Context) {
		key := c.ClientIP()
		now := time.Now()

		rl.mu.Lock()
		cnt, ok := rl.counters[key]
		if !ok || now.After(cnt.windowEnd) {
			cnt = &counter{count: 0, windowEnd: now.Add(rl.window)}
			rl.counters[key] = cnt
		}
		cnt.count++
		exceeded := cnt.count > rl.limit
		rl.mu.Unlock()

		if exceeded {
			response.Fail(c, http.StatusTooManyRequests,
				response.CodeTooManyRequests, "操作过于频繁，请稍后再试")
			return
		}
		c.Next()
	}
}
