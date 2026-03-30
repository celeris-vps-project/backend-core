package http

import (
	"backend-core/internal/identity/app"
	"backend-core/pkg/apperr"
	"backend-core/pkg/authn"
	"context"
	"strings"

	hz_app "github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

type LoginRequest struct {
	Email    string `json:"email" vd:"email"`
	Password string `json:"password" vd:"len($)>5"`
}

type RegisterRequest struct {
	Email    string `json:"email" vd:"email"`
	Password string `json:"password" vd:"len($)>5"`
}

type AuthHandler struct {
	authApp *app.AuthAppService
}

func NewAuthHandler(app *app.AuthAppService) *AuthHandler {
	return &AuthHandler{authApp: app}
}

func (h *AuthHandler) Login(ctx context.Context, c *hz_app.RequestContext) {
	var req LoginRequest

	if err := c.BindAndValidate(&req); err != nil {
		c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeInvalidParams, err.Error()))
		return
	}

	token, role, err := h.authApp.Login(ctx, req.Email, req.Password)
	if err != nil {
		code := classifyAuthError(err)
		c.JSON(consts.StatusUnauthorized, apperr.Resp(code, err.Error()))
		return
	}

	c.JSON(consts.StatusOK, utils.H{
		"message": "login successful",
		"token":   token,
		"role":    role,
	})
}

func (h *AuthHandler) Register(ctx context.Context, c *hz_app.RequestContext) {
	var req RegisterRequest

	if err := c.BindAndValidate(&req); err != nil {
		c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeInvalidParams, err.Error()))
		return
	}

	token, err := h.authApp.RegisterUser(ctx, req.Email, req.Password)
	if err != nil {
		code := classifyRegisterError(err)
		c.JSON(consts.StatusBadRequest, apperr.Resp(code, err.Error()))
		return
	}

	c.JSON(consts.StatusOK, utils.H{
		"message": "register successful",
		"token":   token,
		"role":    "user",
	})
}

func (h *AuthHandler) Me(ctx context.Context, c *hz_app.RequestContext) {
	uid, _ := authn.UserID(c)
	role, ok := authn.UserRole(c)
	if !ok {
		role = "user"
	}
	c.JSON(consts.StatusOK, utils.H{
		"user_id": uid.String(),
		"role":    role,
	})
}

type ChangePasswordRequest struct {
	OldPassword string `json:"old_password" vd:"len($)>0"`
	NewPassword string `json:"new_password" vd:"len($)>5"`
}

func (h *AuthHandler) ChangePassword(ctx context.Context, c *hz_app.RequestContext) {
	var req ChangePasswordRequest
	if err := c.BindAndValidate(&req); err != nil {
		c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeInvalidParams, err.Error()))
		return
	}

	uid, ok := authn.UserID(c)
	if !ok {
		c.JSON(consts.StatusUnauthorized, apperr.Resp(apperr.CodeUnauthorized, "not authenticated"))
		return
	}

	if err := h.authApp.ChangePassword(ctx, uid.String(), req.OldPassword, req.NewPassword); err != nil {
		code := classifyChangePasswordError(err)
		c.JSON(consts.StatusBadRequest, apperr.Resp(code, err.Error()))
		return
	}

	c.JSON(consts.StatusOK, utils.H{
		"message": "password changed successfully",
	})
}

func classifyChangePasswordError(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "old password is incorrect"):
		return apperr.CodeWrongPassword
	case strings.Contains(msg, "user not found"):
		return apperr.CodeUserNotFound
	default:
		return apperr.CodeInternalError
	}
}

func classifyAuthError(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "user not found"):
		return apperr.CodeUserNotFound
	case strings.Contains(msg, "瀵嗙爜閿欒"):
		return apperr.CodeWrongPassword
	case strings.Contains(msg, "domain_error:"):
		return apperr.CodeAccountDisabled
	default:
		return apperr.CodeUnauthorized
	}
}

func classifyRegisterError(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "email already registered"):
		return apperr.CodeEmailTaken
	default:
		return apperr.CodeInternalError
	}
}
