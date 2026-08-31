// Package logger 提供结构化日志，并在输出前对敏感内容做脱敏。
package logger

import (
	"log/slog"
	"os"
	"strings"
)

// Init 初始化全局 logger。
func Init(level string, production bool) {
	var lv slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: lv, ReplaceAttr: redactAttr}

	var h slog.Handler
	if production {
		h = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		h = slog.NewTextHandler(os.Stdout, opts)
	}
	slog.SetDefault(slog.New(h))
}

// redactedKeys 是永不允许出现在日志中的字段名（不区分大小写、支持子串匹配）。
var redactedKeys = []string{
	"password", "passwd", "secret", "token", "api_key", "apikey",
	"authorization", "cookie", "set-cookie", "session", "storage_state",
	"id_card", "idcard", "身份证", "bank", "银行卡", "captcha", "验证码",
	"otp", "mfa", "credential", "private_key",
}

// redactAttr 在日志序列化阶段替换敏感字段的值。
func redactAttr(_ []string, a slog.Attr) slog.Attr {
	lower := strings.ToLower(a.Key)
	for _, k := range redactedKeys {
		if strings.Contains(lower, k) {
			return slog.String(a.Key, "[REDACTED]")
		}
	}
	return a
}
