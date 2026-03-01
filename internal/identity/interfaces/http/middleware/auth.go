package middleware

import (
	"backend-core/internal/identity/infra"
	"context"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

// JWTAuthMiddleware 現在需要接收 JWTService 實例作為依賴
func JWTAuthMiddleware(jwtSvc *infra.JWTService) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		authHeader := c.Request.Header.Get("Authorization")
		if authHeader == "" {
			c.JSON(consts.StatusUnauthorized, utils.H{"error": "未授權：缺失 Authorization Header"})
			c.Abort()
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if !(len(parts) == 2 && parts[0] == "Bearer") {
			c.JSON(consts.StatusUnauthorized, utils.H{"error": "未授權：Token 格式錯誤"})
			c.Abort()
			return
		}

		// 調用真實的 JWT 解析器
		userID, err := jwtSvc.ParseToken(parts[1])
		if err != nil {
			c.JSON(consts.StatusUnauthorized, utils.H{"error": "登入無效或已過期: " + err.Error()})
			c.Abort()
			return
		}

		// 注入真實的 UserID
		c.Set("current_user_id", userID)
		c.Next(ctx)
	}
}
