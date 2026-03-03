package app

import (
	"backend-core/internal/identity/domain"
	"backend-core/internal/identity/infra"
	"fmt"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type mockTokenGenerator struct {
	token    string
	lastUser *domain.User
}

func (m *mockTokenGenerator) Generate(user *domain.User) (string, error) {
	m.lastUser = user
	return m.token, nil
}

func (m *mockTokenGenerator) ParseToken(tokenString string) (string, error) {
	return "generated-id", nil
}

type mockPasswordHasher struct{}

func (m *mockPasswordHasher) Compare(plain, hash string) bool {
	return hash == "hash:"+plain
}

func (m *mockPasswordHasher) Hash(plain string) (string, error) {
	return "hash:" + plain, nil
}

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	if err := db.AutoMigrate(&infra.UserPO{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}

	return db
}

func newTestApp(t *testing.T) (*AuthAppService, *infra.SqliteUserRepo, *mockTokenGenerator) {
	t.Helper()

	db := newTestDB(t)
	repo := infra.NewSqliteUserRepo(db)
	token := &mockTokenGenerator{token: "test-token"}
	hasher := &mockPasswordHasher{}

	return NewAuthAppService(repo, token, hasher), repo, token
}

func newTestAppWithJWT(t *testing.T, secret string) (*AuthAppService, *infra.SqliteUserRepo, *infra.JWTService) {
	t.Helper()

	db := newTestDB(t)
	repo := infra.NewSqliteUserRepo(db)
	jwtSvc := infra.NewJWTService(secret, "test-issuer")
	hasher := &mockPasswordHasher{}

	return NewAuthAppService(repo, jwtSvc, hasher), repo, jwtSvc
}

func TestRegisterUser_Success(t *testing.T) {
	app, repo, _ := newTestApp(t)

	token, err := app.RegisterUser("u@example.com", "pass123")
	if err != nil {
		t.Fatalf("RegisterUser error: %v", err)
	}
	if token != "test-token" {
		t.Fatalf("token mismatch: %s", token)
	}

	user, err := repo.FindByEmail("u@example.com")
	if err != nil {
		t.Fatalf("FindByEmail error: %v", err)
	}
	if user.PasswordHash() != "hash:pass123" {
		t.Fatalf("password hash mismatch: %s", user.PasswordHash())
	}
	if user.Status() != "active" {
		t.Fatalf("status mismatch: %s", user.Status())
	}
}

func TestLogin_Success(t *testing.T) {
	app, repo, _ := newTestApp(t)

	existing := domain.ReconstituteUser("id-1", "login@example.com", "hash:secret", "active")
	if err := repo.Save(existing); err != nil {
		t.Fatalf("Save error: %v", err)
	}

	token, _, err := app.Login("login@example.com", "secret")
	if err != nil {
		t.Fatalf("Login error: %v", err)
	}
	if token != "test-token" {
		t.Fatalf("token mismatch: %s", token)
	}
}

func TestRegisterUser_JWTSignatureValidation(t *testing.T) {
	app, _, jwtSvc := newTestAppWithJWT(t, "secret-1")

	token, err := app.RegisterUser("jwt@example.com", "pass123")
	if err != nil {
		t.Fatalf("RegisterUser error: %v", err)
	}

	userID, err := jwtSvc.ParseToken(token)
	if err != nil {
		t.Fatalf("ParseToken error: %v", err)
	}
	if userID != "generated-id" {
		t.Fatalf("userID mismatch: %s", userID)
	}

	otherSvc := infra.NewJWTService("secret-2", "test-issuer")
	if _, err := otherSvc.ParseToken(token); err == nil {
		t.Fatalf("expected signature validation error")
	}
}
