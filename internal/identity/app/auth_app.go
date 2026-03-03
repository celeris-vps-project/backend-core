package app

import (
	"backend-core/internal/identity/domain"
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
func (app *AuthAppService) Login(email, plainPassword string) (string, string, error) {
	// 1. 获取实体
	user, err := app.repo.FindByEmail(email)
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

func (app *AuthAppService) RegisterUser(email, plainPassword string) (string, error) {
	// 1. 生成密码哈希
	hash, err := app.hasher.Hash(plainPassword)
	if err != nil {
		return "", err
	}

	// 2. 创建新用户实体（这里简化了 ID 和状态的生成）
	newUser := domain.ReconstituteUser("generated-id", email, hash, "active")

	// 3. 保存用户到数据库（需要在 UserRepository 中实现 Save 方法）
	if err := app.repo.Save(newUser); err != nil {
		return "", err
	}

	// 4. 生成并返回 Token
	return app.token.Generate(newUser)
}
