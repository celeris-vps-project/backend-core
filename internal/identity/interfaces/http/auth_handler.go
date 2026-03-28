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

// LoginRequest 定义 Hertz 的数据绑定和校验规则
type LoginRequest struct {
	Email    string `json:"email" vd:"email"`       // vd 是 Hertz 内置的 validator 标签
	Password string `json:"password" vd:"len($)>5"` // 密码长度必须大于 5
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

// Login 处理登录 HTTP 请求
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
		"message": "登录成功",
		"token":   token,
		"role":    role,
	})
}

// Register 处理注册 HTTP 请求
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
		"message": "注册成功",
		"token":   token,
		"role":    "user",
	})
}

// Me returns the current user's profile (extracted from JWT via middleware).
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

// ChangePasswordRequest defines the request body for password change.
type ChangePasswordRequest struct {
	OldPassword string `json:"old_password" vd:"len($)>0"`
	NewPassword string `json:"new_password" vd:"len($)>5"`
}

// ChangePassword handles PUT /api/v1/me/password — allows authenticated users
// to change their own password by providing the current and new password.
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
		"message": "密码修改成功",
	})
}

// classifyChangePasswordError maps password-change errors to an error code.
func classifyChangePasswordError(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "旧密码不正确"):
		return apperr.CodeWrongPassword
	case strings.Contains(msg, "user not found"):
		return apperr.CodeUserNotFound
	default:
		return apperr.CodeInternalError
	}
}

// classifyAuthError maps login domain/infra errors to an error code.
func classifyAuthError(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "user not found"):
		return apperr.CodeUserNotFound
	case strings.Contains(msg, "密码错误"):
		return apperr.CodeWrongPassword
	case strings.Contains(msg, "封禁"), strings.Contains(msg, "未激活"):
		return apperr.CodeAccountDisabled
	default:
		return apperr.CodeUnauthorized
	}
}

// classifyRegisterError maps registration errors to an error code.
func classifyRegisterError(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "已被注册"):
		return apperr.CodeEmailTaken
	default:
		return apperr.CodeInternalError
	}
}
