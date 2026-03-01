package http

import (
	"backend-core/internal/indentity/application"
	"context"

	"github.com/cloudwego/hertz/pkg/app"
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
	authApp *application.AuthAppService
}

func NewAuthHandler(app *application.AuthAppService) *AuthHandler {
	return &AuthHandler{authApp: app}
}

// Login 处理登录 HTTP 请求
// 注意 Hertz 的签名规范：(ctx context.Context, c *app.RequestContext)
func (h *AuthHandler) Login(ctx context.Context, c *app.RequestContext) {
	var req LoginRequest

	// 1. 绑定参数并校验 (如果非 email 格式或密码太短，直接报错返回)
	if err := c.BindAndValidate(&req); err != nil {
		c.JSON(consts.StatusBadRequest, utils.H{"error": "参数格式错误: " + err.Error()})
		return
	}

	// 2. 调用应用层
	token, err := h.authApp.Login(req.Email, req.Password)
	if err != nil {
		// 这里可以根据具体的 error 类型返回 401 或 403，目前简化处理
		c.JSON(consts.StatusUnauthorized, utils.H{"error": err.Error()})
		return
	}

	// 3. 组装成功响应
	c.JSON(consts.StatusOK, utils.H{
		"message": "登录成功",
		"token":   token,
	})
}

// Register 处理注册 HTTP 请求
func (h *AuthHandler) Register(ctx context.Context, c *app.RequestContext) {
	var req RegisterRequest

	if err := c.BindAndValidate(&req); err != nil {
		c.JSON(consts.StatusBadRequest, utils.H{"error": "参数格式错误: " + err.Error()})
		return
	}

	token, err := h.authApp.RegisterUser(req.Email, req.Password)
	if err != nil {
		c.JSON(consts.StatusBadRequest, utils.H{"error": err.Error()})
		return
	}

	c.JSON(consts.StatusOK, utils.H{
		"message": "注册成功",
		"token":   token,
	})
}
