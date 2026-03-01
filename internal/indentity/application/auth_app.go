package application

import (
	"backend-core/internal/indentity/domain"
)

// 依赖的外部服务接口 (由基础设施层实现)
type TokenGenerator interface {
	Generate(user *domain.User) (string, error)
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

// Login 登录用例
func (app *AuthAppService) Login(email, plainPassword string) (string, error) {
	// 1. 获取实体
	user, err := app.repo.FindByEmail(email)
	if err != nil {
		return "", err
	}

	// 2. 执行领域对象的鉴权规则
	if err := user.Authenticate(plainPassword, app.hasher.Compare); err != nil {
		return "", err
	}

	// 3. 生成并返回 Token
	return app.token.Generate(user)
}
