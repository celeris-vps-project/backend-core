package app

import (
	"backend-core/internal/identity/domain"
	"context"
	"crypto/rand"
	"errors"
	"log"
	"math/big"

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

// ChangePassword allows an authenticated user to change their password.
// It verifies the old password, hashes the new one, and persists the change.
func (app *AuthAppService) ChangePassword(ctx context.Context, userID, oldPassword, newPassword string) error {
	// 1. Look up the user by ID
	user, err := app.repo.FindByID(ctx, userID)
	if err != nil {
		return errors.New("user not found")
	}

	// 2. Verify old password
	if !app.hasher.Compare(oldPassword, user.PasswordHash()) {
		return errors.New("旧密码不正确")
	}

	// 3. Hash new password
	newHash, err := app.hasher.Hash(newPassword)
	if err != nil {
		return err
	}

	// 4. Persist
	return app.repo.UpdatePasswordHash(ctx, userID, newHash)
}

// EnsureAdmin checks if an admin account exists with the given email.
// If not, it creates one with a random 12-character password and logs it
// exactly once. This should be called once during server startup.
func (app *AuthAppService) EnsureAdmin(ctx context.Context, email string) {
	if email == "" {
		email = "admin@celeris.local"
	}

	// Check if admin already exists
	if _, err := app.repo.FindByEmail(ctx, email); err == nil {
		log.Printf("[api] admin account already exists (%s), skipping seed", email)
		return
	}

	// Generate random 12-character password
	password := generateRandomPassword(12)

	// Hash it
	hash, err := app.hasher.Hash(password)
	if err != nil {
		log.Printf("[api] FATAL: failed to hash admin password: %v", err)
		return
	}

	// Create admin user with role=admin
	adminUser := domain.ReconstituteUserWithRole(uuid.New().String(), email, hash, "active", domain.RoleAdmin)
	if err := app.repo.Save(ctx, adminUser); err != nil {
		log.Printf("[api] FATAL: failed to create admin account: %v", err)
		return
	}

	// Print credentials exactly once — the password is never stored in plaintext
	log.Printf("[api] ═══════════════════════════════════════════════════════")
	log.Printf("[api]  ADMIN ACCOUNT CREATED")
	log.Printf("[api]  Email:    %s", email)
	log.Printf("[api]  Password: %s", password)
	log.Printf("[api]  ⚠ This password will NOT be shown again. Save it now!")
	log.Printf("[api] ═══════════════════════════════════════════════════════")
}

// generateRandomPassword creates a cryptographically random password of the
// given length using alphanumeric characters plus a few symbols.
func generateRandomPassword(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%"
	result := make([]byte, length)
	for i := range result {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			// Fallback — should never happen with crypto/rand
			result[i] = charset[i%len(charset)]
			continue
		}
		result[i] = charset[n.Int64()]
	}
	return string(result)
}
