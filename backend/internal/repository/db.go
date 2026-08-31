// Package repository 是唯一允许直接访问数据库的层。
//
// 安全约定：
//   - 全部查询使用 GORM 参数绑定，禁止字符串拼接用户输入；
//   - 排序字段等必须出现在 SQL 结构中的标识符，只能取自白名单。
package repository

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// Open 建立数据库连接池。
func Open(ctx context.Context, dsn string, debug bool) (*gorm.DB, error) {
	level := gormlogger.Warn
	if debug {
		level = gormlogger.Info
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger:                 gormlogger.Default.LogMode(level),
		SkipDefaultTransaction: true,
		NowFunc:                func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		return nil, fmt.Errorf("连接数据库失败: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(25)
	sqlDB.SetMaxIdleConns(5)
	sqlDB.SetConnMaxLifetime(time.Hour)

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		return nil, fmt.Errorf("数据库 ping 失败: %w", err)
	}

	slog.Info("数据库连接就绪")
	return db, nil
}

// Store 聚合所有仓储，方便注入。
type Store struct {
	DB          *gorm.DB
	User        *UserRepo
	Job         *JobRepo
	SearchTask  *SearchTaskRepo
	Cart        *CartRepo
	Application *ApplicationRepo
	Browser     *BrowserRepo
	Resume      *ResumeRepo
	Site        *SiteRecipeRepo
}

// NewStore 构造仓储集合。
func NewStore(db *gorm.DB) *Store {
	return &Store{
		DB:          db,
		User:        &UserRepo{db: db},
		Job:         &JobRepo{db: db},
		SearchTask:  &SearchTaskRepo{db: db},
		Cart:        &CartRepo{db: db},
		Application: &ApplicationRepo{db: db},
		Browser:     &BrowserRepo{db: db},
		Resume:      &ResumeRepo{db: db},
		Site:        newSiteRecipeRepo(db),
	}
}

// Tx 在事务中执行 fn。
func (s *Store) Tx(ctx context.Context, fn func(tx *Store) error) error {
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(NewStore(tx))
	})
}
