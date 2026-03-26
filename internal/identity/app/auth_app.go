package app

import (
	"backend-core/internal/identity/domain"
	"context"
	"errors"

	"github.com/google/uuid"
)

// 依赖的外部服务接口 (由基础设施层实现)
type TokenGenerator interface {
	Generate(user *domain.User) (string, error)
	ParseToken(tokenString string) (string, error) // 解析 Token 返回 UserID
}

type PasswordHasher interface {
	Compare(plain, hash string) bool
	Hash(plain string) (string, error) // 留给未来的 RegisterUser 用例
}

type AuthAppService struct {
	repo   domain.UserRepository
	token  TokenGenerator
	hasher PasswordHasher
}

func NewAuthAppService(r domain.UserRepository, t TokenGenerator, h PasswordHasher) *AuthAppService {
	return &AuthAppService{repo: r, token: t, hasher: h}
}

// Login 登录用例 — returns token and role
func (app *AuthAppService) Login(ctx context.Context, email, plainPassword string) (string, string, error) {
	// 1. 获取实体
	user, err := app.repo.FindByEmail(ctx, email)
	if err != nil {
		return "", "", err
	}

	// 2. 执行领域对象的鉴权规则
	if err := user.Authenticate(plainPassword, app.hasher.Compare); err != nil {
		return "", "", err
	}

	// 3. 生成并返回 Token
	token, err := app.token.Generate(user)
	if err != nil {
		return "", "", err
	}
	return token, user.Role(), nil
}

func (app *AuthAppService) RegisterUser(ctx context.Context, email, plainPassword string) (string, error) {
	// 1. 检查邮箱是否已被注册
	if _, err := app.repo.FindByEmail(ctx, email); err == nil {
		return "", errors.New("该邮箱已被注册")
	}

	// 2. 生成密码哈希
	hash, err := app.hasher.Hash(plainPassword)
	if err != nil {
		return "", err
	}

	// 3. 生成全局唯一 UUID 作为用户 ID
	newUser := domain.ReconstituteUser(uuid.New().String(), email, hash, "active")

	// 4. 保存用户到数据库
	if err := app.repo.Save(ctx, newUser); err != nil {
		return "", err
	}

	// 5. 生成并返回 Token
	return app.token.Generate(newUser)
}
