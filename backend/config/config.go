// Package config 负责集中加载与校验环境变量。
//
// 安全原则：
//  1. 必填项缺失时 fail-fast，绝不提供默认凭据；
//  2. 密钥只从环境变量读取，不落配置文件、不打日志；
//  3. 所有外部调用超时时间均可配置且有合理上限。
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config 是应用运行期的全部配置。
type Config struct {
	Env        string
	LogLevel   string
	ServerPort int

	DatabaseURL string
	RedisURL    string

	LLM    LLMConfig
	Tavily TavilyConfig

	BrowserWorkerURL   string
	BrowserWorkerToken string

	StorageDir  string
	MaxUploadMB int64

	CORSAllowedOrigins []string
	AllowOutboundFetch bool
}

// LLMConfig 描述 LLM 供应商配置。APIKey 为空表示降级为纯规则模式。
// 兼容任意 OpenAI Chat Completions 协议的供应商（DeepSeek / 阿里云百炼-千问等）。
type LLMConfig struct {
	APIKey  string
	BaseURL string
	Model   string
	Timeout time.Duration
}

// Enabled 报告是否可以调用 LLM。
func (c LLMConfig) Enabled() bool { return c.APIKey != "" }

// TavilyConfig 描述搜索供应商配置。
type TavilyConfig struct {
	APIKey  string
	BaseURL string
	Timeout time.Duration
}

// Enabled 报告是否可以调用搜索接口。
func (c TavilyConfig) Enabled() bool { return c.APIKey != "" }

// IsProduction 报告当前是否生产环境。
func (c *Config) IsProduction() bool { return c.Env == "production" }

// Load 从环境变量构建配置，必填项缺失即返回错误。
func Load() (*Config, error) {
	cfg := &Config{
		Env:         getEnv("APP_ENV", "development"),
		LogLevel:    getEnv("LOG_LEVEL", "info"),
		ServerPort:  getEnvInt("SERVER_PORT", 9090),
		DatabaseURL: os.Getenv("DATABASE_URL"),
		RedisURL:    os.Getenv("REDIS_URL"),
		// LLM 供应商：优先使用千问（QWEN_API_KEY），未配置时回退 DeepSeek。
		// 两者均为 OpenAI Chat Completions 兼容协议，仅需切换 BaseURL / Model。
		LLM: loadLLMConfig(),
		Tavily: TavilyConfig{
			APIKey:  os.Getenv("TAVILY_API_KEY"),
			BaseURL: getEnv("TAVILY_BASE_URL", "https://api.tavily.com"),
			Timeout: getEnvSeconds("TAVILY_TIMEOUT_SECONDS", 30*time.Second, 2*time.Minute),
		},
		BrowserWorkerURL:   getEnv("BROWSER_WORKER_URL", "http://127.0.0.1:8390"),
		BrowserWorkerToken: os.Getenv("BROWSER_WORKER_TOKEN"),
		StorageDir:         getEnv("STORAGE_DIR", "./storage/resumes"),
		MaxUploadMB:        int64(getEnvInt("MAX_UPLOAD_MB", 10)),
		CORSAllowedOrigins: splitAndTrim(getEnv("CORS_ALLOWED_ORIGINS", "http://localhost:3000")),
		AllowOutboundFetch: getEnvBool("ALLOW_OUTBOUND_FETCH", true),
	}

	var missing []string
	if cfg.DatabaseURL == "" {
		missing = append(missing, "DATABASE_URL")
	}
	if cfg.RedisURL == "" {
		missing = append(missing, "REDIS_URL")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("缺少必需的环境变量: %s", strings.Join(missing, ", "))
	}

	if cfg.MaxUploadMB <= 0 || cfg.MaxUploadMB > 50 {
		return nil, errors.New("MAX_UPLOAD_MB 必须位于 1..50 之间")
	}
	if len(cfg.CORSAllowedOrigins) == 0 {
		return nil, errors.New("CORS_ALLOWED_ORIGINS 不能为空（禁止反射任意 Origin）")
	}
	for _, o := range cfg.CORSAllowedOrigins {
		if o == "*" {
			return nil, errors.New("CORS_ALLOWED_ORIGINS 不允许使用通配符 *")
		}
	}
	if cfg.IsProduction() && cfg.BrowserWorkerToken == "" {
		return nil, errors.New("生产环境必须设置 BROWSER_WORKER_TOKEN")
	}

	return cfg, nil
}

func getEnv(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// loadLLMConfig 构建 LLM 供应商配置。
// 优先千问（QWEN_API_KEY，阿里云百炼 OpenAI 兼容接口），未配置时回退 DeepSeek。
// APIKey 留空 → Enabled() 为 false → 业务自动降级为纯规则模式。
func loadLLMConfig() LLMConfig {
	if key := strings.TrimSpace(os.Getenv("QWEN_API_KEY")); key != "" {
		return LLMConfig{
			APIKey:  key,
			BaseURL: getEnv("QWEN_BASE_URL", "https://dashscope.aliyuncs.com/compatible-mode/v1"),
			Model:   getEnv("QWEN_MODEL", "glm-5.2"),
			Timeout: getEnvSeconds("QWEN_TIMEOUT_SECONDS", 90*time.Second, 5*time.Minute),
		}
	}
	return LLMConfig{
		APIKey:  strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY")),
		BaseURL: getEnv("DEEPSEEK_BASE_URL", "https://api.deepseek.com"),
		Model:   getEnv("DEEPSEEK_MODEL", "deepseek-chat"),
		Timeout: getEnvSeconds("DEEPSEEK_TIMEOUT_SECONDS", 90*time.Second, 5*time.Minute),
	}
}

func getEnvInt(key string, def int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func getEnvBool(key string, def bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

// getEnvSeconds 读取以秒为单位的配置，并强制不超过 max。
func getEnvSeconds(key string, def, max time.Duration) time.Duration {
	n := getEnvInt(key, int(def.Seconds()))
	d := time.Duration(n) * time.Second
	if d <= 0 || d > max {
		return def
	}
	return d
}

func splitAndTrim(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
