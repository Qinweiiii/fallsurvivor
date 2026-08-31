// Package migrations 通过 embed 暴露 SQL 迁移文件。
package migrations

import "embed"

// FS 包含全部 .sql 迁移文件。
//
//go:embed *.sql
var FS embed.FS
