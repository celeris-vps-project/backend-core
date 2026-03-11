// Package authn provides shared HTTP authentication context helpers.
//
// The auth middleware writes user identity into the Hertz RequestContext;
// handlers across all domains read it back via the helper functions here.
// This avoids cross-domain imports between internal modules.
package authn

import (
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/google/uuid"
)

// Context key constants — written by the auth middleware, read by handlers.
const (
	ContextKeyUserID   = "current_user_id"
	ContextKeyUserRole = "current_user_role"
)

// UserID extracts the authenticated user's UUID from the Hertz context.
// Returns a zero UUID and false if the user is not authenticated.
func UserID(ctx *app.RequestContext) (uuid.UUID, bool) {
	v, exists := ctx.Get(ContextKeyUserID)
	if !exists {
		return uuid.UUID{}, false
	}
	uid, ok := v.(uuid.UUID)
	return uid, ok && uid != uuid.UUID{}
}

// UserRole extracts the authenticated user's role from the Hertz context.
func UserRole(ctx *app.RequestContext) (string, bool) {
	v, exists := ctx.Get(ContextKeyUserRole)
	if !exists {
		return "", false
	}
	role, ok := v.(string)
	return role, ok
}
