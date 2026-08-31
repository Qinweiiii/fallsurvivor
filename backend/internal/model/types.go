// Package model 定义与数据库表一一对应的 GORM 模型。
package model

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// JSONStringArray 映射 PostgreSQL JSONB 数组，用于字符串列表字段。
type JSONStringArray []string

// Scan 实现 sql.Scanner。
func (a *JSONStringArray) Scan(value any) error {
	if value == nil {
		*a = JSONStringArray{}
		return nil
	}
	var b []byte
	switch v := value.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return fmt.Errorf("JSONStringArray: 不支持的类型 %T", value)
	}
	if len(b) == 0 {
		*a = JSONStringArray{}
		return nil
	}
	var out []string
	if err := json.Unmarshal(b, &out); err != nil {
		return err
	}
	*a = out
	return nil
}

// Value 实现 driver.Valuer。
func (a JSONStringArray) Value() (driver.Value, error) {
	if a == nil {
		return []byte("[]"), nil
	}
	return json.Marshal([]string(a))
}

// MarshalJSON 保证 nil 值序列化为 [] 而不是 null。
//
// 前端会对这些数组直接调用 .length / .map，
// null 会导致 TypeError，因此这里是必要的兜底。
func (a JSONStringArray) MarshalJSON() ([]byte, error) {
	if a == nil {
		return []byte("[]"), nil
	}
	return json.Marshal([]string(a))
}

// UnmarshalJSON 保证空值反序列化为空数组而非 nil。
func (a *JSONStringArray) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*a = JSONStringArray{}
		return nil
	}
	var out []string
	if err := json.Unmarshal(b, &out); err != nil {
		return err
	}
	*a = out
	return nil
}

// JSONMap 映射 PostgreSQL JSONB 对象。
//
// 注意：仅用于确定性结构已知的辅助字段（如 match_analysis）。
// 反序列化外部不可信数据时必须使用具体的 DTO 结构体，不得使用本类型。
type JSONMap map[string]any

// Scan 实现 sql.Scanner。
func (m *JSONMap) Scan(value any) error {
	if value == nil {
		*m = JSONMap{}
		return nil
	}
	var b []byte
	switch v := value.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return errors.New("JSONMap: 不支持的类型")
	}
	if len(b) == 0 {
		*m = JSONMap{}
		return nil
	}
	out := map[string]any{}
	if err := json.Unmarshal(b, &out); err != nil {
		return err
	}
	*m = out
	return nil
}

// Value 实现 driver.Valuer。
func (m JSONMap) Value() (driver.Value, error) {
	if m == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(map[string]any(m))
}

// JSONBOf 把任意结构体序列化为可直接写入 JSONB 列的字节串。
func JSONBOf(v any) ([]byte, error) {
	if v == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(v)
}

// UUID 别名，便于统一替换。
type ID = uuid.UUID

// NewID 生成新的主键。
func NewID() ID { return uuid.New() }

// Timestamps 是通用时间字段。
type Timestamps struct {
	CreatedAt time.Time `json:"created_at" gorm:"column:created_at;autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"column:updated_at;autoUpdateTime"`
}
