package middleware

import (
	"backend-core/internal/identity/infra"
	"backend-core/pkg/authn"
	"context"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/google/uuid"
)

// JWTAuthMiddleware 驗證請求攜帶的 Bearer Token，並將解析後的用戶信息注入上下文
func JWTAuthMiddleware(jwtSvc *infra.JWTService) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		authHeader := c.Request.Header.Get("Authorization")
		if authHeader == "" {
			c.JSON(consts.StatusUnauthorized, utils.H{"error": "未授權：缺失 Authorization Header"})
			c.Abort()
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			c.JSON(consts.StatusUnauthorized, utils.H{"error": "未授權：Token 格式錯誤"})
			c.Abort()
			return
		}

		// 解析 JWT，同時提取 userID 與 role
		userIDStr, role, err := jwtSvc.ParseTokenWithRole(parts[1])
		if err != nil {
			c.JSON(consts.StatusUnauthorized, utils.H{"error": "登入無效或已過期: " + err.Error()})
			c.Abort()
			return
		}

		// 將 sub 字段解析為強類型 UUID，防止非法值流入下游業務邏輯
		userID, err := uuid.Parse(userIDStr)
		if err != nil {
			c.JSON(consts.StatusUnauthorized, utils.H{"error": "Token 中的用戶 ID 格式無效"})
			c.Abort()
			return
		}

		// 注入強類型 UUID 與角色信息，下游 Handler 直接斷言使用
		c.Set(authn.ContextKeyUserID, userID)
		c.Set(authn.ContextKeyUserRole, role)
		c.Next(ctx)
	}
}
