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

// AdminMiddleware ensures the caller has an "admin" role in their JWT.
// It extracts both userID and role, sets them in the request context,
// and rejects non-admin users with 403 Forbidden.
func AdminMiddleware(jwtSvc *infra.JWTService) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		authHeader := c.Request.Header.Get("Authorization")
		if authHeader == "" {
			c.JSON(consts.StatusUnauthorized, utils.H{"error": "missing Authorization header"})
			c.Abort()
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if !(len(parts) == 2 && parts[0] == "Bearer") {
			c.JSON(consts.StatusUnauthorized, utils.H{"error": "invalid token format"})
			c.Abort()
			return
		}

		userIDStr, role, err := jwtSvc.ParseTokenWithRole(parts[1])
		if err != nil {
			c.JSON(consts.StatusUnauthorized, utils.H{"error": "invalid or expired token: " + err.Error()})
			c.Abort()
			return
		}

		if role != "admin" {
			c.JSON(consts.StatusForbidden, utils.H{"error": "admin access required"})
			c.Abort()
			return
		}

		// Parse string → uuid.UUID so downstream handlers can use authn.UserID()
		userID, parseErr := uuid.Parse(userIDStr)
		if parseErr != nil {
			c.JSON(consts.StatusUnauthorized, utils.H{"error": "Token 中的用戶 ID 格式無效"})
			c.Abort()
			return
		}

		c.Set(authn.ContextKeyUserID, userID)
		c.Set(authn.ContextKeyUserRole, role)
		c.Next(ctx)
	}
}
