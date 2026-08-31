// Package handler 是 HTTP 处理层。
//
// 约定：handler 只做「参数绑定 + 校验 + 调用 service + 组装响应」，
// 绝不直接访问数据库，也不承载业务规则。
package handler

import (
	"context"
	"errors"
	"log/slog"

	"github.com/gin-gonic/gin"

	"github.com/eddiel/fallsurvivor/backend/internal/model"
	"github.com/eddiel/fallsurvivor/backend/internal/repository"
	"github.com/eddiel/fallsurvivor/backend/pkg/response"
)

// currentUser 返回当前用户 ID。
//
// 第一版为单用户本地工具，用户身份由服务端在启动时确定，
// 不接受客户端传入的任何用户标识，因此不存在越权取数的入口。
// 后续若支持多用户，此处替换为从会话中读取已认证身份。
type userResolver struct {
	store *repository.Store
	// cached 缓存默认用户 ID，避免每次请求查库。
	cached *model.ID
}

func newUserResolver(store *repository.Store) *userResolver {
	return &userResolver{store: store}
}

// resolve 返回当前用户 ID。
func (r *userResolver) resolve(ctx context.Context) (model.ID, error) {
	if r.cached != nil {
		return *r.cached, nil
	}
	u, err := r.store.User.GetDefaultUser(ctx)
	if err != nil {
		return model.ID{}, err
	}
	id := u.ID
	r.cached = &id
	return id, nil
}

// requireUser 解析当前用户，失败时直接写响应并返回 false。
func (r *userResolver) requireUser(c *gin.Context) (model.ID, bool) {
	id, err := r.resolve(c.Request.Context())
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			response.Fail(c, 503, response.CodeInternal, "系统尚未初始化，请先执行 make seed")
			return model.ID{}, false
		}
		logErr(c, "解析当前用户失败", err)
		response.Internal(c)
		return model.ID{}, false
	}
	return id, true
}

// logErr 记录错误详情到服务端日志。
func logErr(c *gin.Context, msg string, err error) {
	slog.Error(msg,
		"error", err.Error(),
		"path", c.Request.URL.Path,
		"request_id", c.GetString("request_id"))
}

// handleServiceError 把 service 层错误映射为 HTTP 响应。
// 只向客户端返回通用文案，细节写日志。
func handleServiceError(c *gin.Context, err error) {
	if errors.Is(err, repository.ErrNotFound) {
		response.NotFound(c, "")
		return
	}
	logErr(c, "业务处理失败", err)
	response.Internal(c)
}
