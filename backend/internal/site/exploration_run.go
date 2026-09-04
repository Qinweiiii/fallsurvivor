package site

import (
	"encoding/json"
	"time"
)

// ExplorationRun 保存一次站点探索的结果；即使没有产生 Recipe 也保留轨迹。
type ExplorationRun struct {
	ID         string          `json:"id" gorm:"column:id;primaryKey;type:uuid;default:gen_random_uuid()"`
	SiteKey    string          `json:"site_key" gorm:"column:site_key;type:text;not null"`
	Keyword    string          `json:"keyword" gorm:"column:keyword;type:text;not null;default:''"`
	EntryURL   string          `json:"entry_url" gorm:"column:entry_url;type:text;not null;default:''"`
	Status     string          `json:"status" gorm:"column:status;type:varchar(20);not null"`
	Reason     string          `json:"reason" gorm:"column:reason;type:text;not null;default:''"`
	Trace      json.RawMessage `json:"trace" gorm:"column:trace;type:jsonb;serializer:json"`
	DurationMS int64           `json:"duration_ms" gorm:"column:duration_ms;not null;default:0"`
	CreatedAt  time.Time       `json:"created_at" gorm:"column:created_at;not null;default:now()"`
}

// TableName 指定探索运行记录表。
func (ExplorationRun) TableName() string { return "exploration_runs" }
