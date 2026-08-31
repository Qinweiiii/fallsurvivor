// Command migrate 执行数据库迁移与初始化数据。
//
// 用法：
//
//	go run ./cmd/migrate up     执行全部未应用的迁移
//	go run ./cmd/migrate down   回滚最后一个已应用的迁移
//	go run ./cmd/migrate status 查看迁移状态
//	go run ./cmd/migrate seed   写入默认用户与求职画像（幂等）
package main

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/eddiel/fallsurvivor/backend/config"
	"github.com/eddiel/fallsurvivor/backend/internal/model"
	"github.com/eddiel/fallsurvivor/backend/internal/repository"
	"github.com/eddiel/fallsurvivor/backend/internal/service/profile"
	"github.com/eddiel/fallsurvivor/backend/migrations"
	"github.com/eddiel/fallsurvivor/backend/pkg/logger"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "执行失败: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 {
		return fmt.Errorf("请指定子命令：up | down | status | seed")
	}
	cmd := os.Args[1]

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger.Init(cfg.LogLevel, false)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	db, err := repository.Open(ctx, cfg.DatabaseURL, false)
	if err != nil {
		return err
	}

	if err := ensureMigrationTable(db); err != nil {
		return err
	}

	switch cmd {
	case "up":
		return migrateUp(db)
	case "down":
		return migrateDown(db)
	case "status":
		return migrateStatus(db)
	case "seed":
		return seed(ctx, db)
	default:
		return fmt.Errorf("未知子命令: %s", cmd)
	}
}

// migrationRecord 记录已应用的迁移。
type migrationRecord struct {
	Version   string    `gorm:"column:version;primaryKey"`
	AppliedAt time.Time `gorm:"column:applied_at"`
}

// TableName 指定表名。
func (migrationRecord) TableName() string { return "schema_migrations" }

func ensureMigrationTable(db *gorm.DB) error {
	return db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version    VARCHAR(200) PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`).Error
}

// migrationFile 是一个迁移文件。
type migrationFile struct {
	Version  string
	UpPath   string
	DownPath string
}

// loadMigrations 扫描嵌入的迁移文件。
func loadMigrations() ([]migrationFile, error) {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return nil, err
	}

	byVersion := map[string]*migrationFile{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		var version, kind string
		switch {
		case strings.HasSuffix(name, ".up.sql"):
			version = strings.TrimSuffix(name, ".up.sql")
			kind = "up"
		case strings.HasSuffix(name, ".down.sql"):
			version = strings.TrimSuffix(name, ".down.sql")
			kind = "down"
		default:
			continue
		}

		m, ok := byVersion[version]
		if !ok {
			m = &migrationFile{Version: version}
			byVersion[version] = m
		}
		full := name
		if kind == "up" {
			m.UpPath = full
		} else {
			m.DownPath = full
		}
	}

	out := make([]migrationFile, 0, len(byVersion))
	for _, m := range byVersion {
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

func appliedVersions(db *gorm.DB) (map[string]bool, error) {
	var records []migrationRecord
	if err := db.Find(&records).Error; err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(records))
	for _, r := range records {
		out[r.Version] = true
	}
	return out, nil
}

func migrateUp(db *gorm.DB) error {
	files, err := loadMigrations()
	if err != nil {
		return err
	}
	applied, err := appliedVersions(db)
	if err != nil {
		return err
	}

	count := 0
	for _, m := range files {
		if applied[m.Version] || m.UpPath == "" {
			continue
		}
		sqlBytes, err := migrations.FS.ReadFile(m.UpPath)
		if err != nil {
			return err
		}

		slog.Info("应用迁移", "version", m.Version)
		err = db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec(string(sqlBytes)).Error; err != nil {
				return err
			}
			return tx.Create(&migrationRecord{Version: m.Version, AppliedAt: time.Now().UTC()}).Error
		})
		if err != nil {
			return fmt.Errorf("迁移 %s 失败: %w", m.Version, err)
		}
		count++
	}

	if count == 0 {
		slog.Info("没有需要应用的迁移")
	} else {
		slog.Info("迁移完成", "applied", count)
	}
	return nil
}

func migrateDown(db *gorm.DB) error {
	files, err := loadMigrations()
	if err != nil {
		return err
	}
	applied, err := appliedVersions(db)
	if err != nil {
		return err
	}

	// 找到最后一个已应用的迁移。
	for i := len(files) - 1; i >= 0; i-- {
		m := files[i]
		if !applied[m.Version] {
			continue
		}
		if m.DownPath == "" {
			return fmt.Errorf("迁移 %s 没有对应的回滚脚本", m.Version)
		}
		sqlBytes, err := migrations.FS.ReadFile(m.DownPath)
		if err != nil {
			return err
		}

		slog.Info("回滚迁移", "version", m.Version)
		return db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec(string(sqlBytes)).Error; err != nil {
				return err
			}
			return tx.Where("version = ?", m.Version).Delete(&migrationRecord{}).Error
		})
	}

	slog.Info("没有可回滚的迁移")
	return nil
}

func migrateStatus(db *gorm.DB) error {
	migrationList, err := loadMigrations()
	if err != nil {
		return err
	}
	applied, err := appliedVersions(db)
	if err != nil {
		return err
	}
	for _, m := range migrationList {
		state := "待应用"
		if applied[m.Version] {
			state = "已应用"
		}
		fmt.Printf("  %-20s %s\n", m.Version, state)
	}
	return nil
}

// seed 写入默认用户与求职画像。多次执行幂等。
func seed(ctx context.Context, db *gorm.DB) error {
	store := repository.NewStore(db)

	user, err := store.User.GetDefaultUser(ctx)
	if err != nil {
		user = &model.UserProfile{Name: "我"}
		if err := store.User.CreateUser(ctx, user); err != nil {
			return err
		}
		slog.Info("已创建默认用户", "id", user.ID.String())
	} else {
		slog.Info("默认用户已存在", "id", user.ID.String())
	}

	if _, err := store.User.GetProfile(ctx, user.ID); err != nil {
		p := profile.DefaultProfile(user.ID)
		if err := store.User.UpsertProfile(ctx, p); err != nil {
			return err
		}
		slog.Info("已创建默认求职画像")
	} else {
		slog.Info("求职画像已存在，未做修改")
	}

	if _, err := store.User.GetApplicationProfile(ctx, user.ID); err != nil {
		if err := store.User.UpsertApplicationProfile(ctx, &model.ApplicationProfile{
			UserID:      user.ID,
			ProfileData: model.JSONMap{},
		}); err != nil {
			return err
		}
		slog.Info("已初始化申请信息")
	}

	slog.Info("初始化完成")
	return nil
}
