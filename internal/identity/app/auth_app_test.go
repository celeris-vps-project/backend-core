package app

import (
	"backend-core/internal/identity/domain"
	"backend-core/internal/identity/infra"
	"context"
	"errors"
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

type mockRegistrationNotifier struct {
	calls  int
	userID string
	email  string
	err    error
}

func (m *mockRegistrationNotifier) NotifyUserRegistered(ctx context.Context, userID, email string) error {
	m.calls++
	m.userID = userID
	m.email = email
	return m.err
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

func newTestApp(t *testing.T) (*AuthAppService, *infra.GormUserRepo, *mockTokenGenerator, *mockRegistrationNotifier) {
	t.Helper()

	db := newTestDB(t)
	repo := infra.NewGormUserRepo(db)
	token := &mockTokenGenerator{token: "test-token"}
	hasher := &mockPasswordHasher{}
	notifier := &mockRegistrationNotifier{}

	return NewAuthAppService(repo, token, hasher, notifier), repo, token, notifier
}

func newTestAppWithJWT(t *testing.T, secret string) (*AuthAppService, *infra.GormUserRepo, *infra.JWTService) {
	t.Helper()

	db := newTestDB(t)
	repo := infra.NewGormUserRepo(db)
	jwtSvc := infra.NewJWTService(secret, "test-issuer")
	hasher := &mockPasswordHasher{}

	return NewAuthAppService(repo, jwtSvc, hasher, nil), repo, jwtSvc
}

func TestRegisterUser_Success(t *testing.T) {
	app, repo, _, notifier := newTestApp(t)
	ctx := context.Background()

	token, err := app.RegisterUser(ctx, "u@example.com", "pass123")
	if err != nil {
		t.Fatalf("RegisterUser error: %v", err)
	}
	if token != "test-token" {
		t.Fatalf("token mismatch: %s", token)
	}

	user, err := repo.FindByEmail(ctx, "u@example.com")
	if err != nil {
		t.Fatalf("FindByEmail error: %v", err)
	}
	if user.PasswordHash() != "hash:pass123" {
		t.Fatalf("password hash mismatch: %s", user.PasswordHash())
	}
	if user.Status() != "active" {
		t.Fatalf("status mismatch: %s", user.Status())
	}
	if notifier.calls != 1 {
		t.Fatalf("expected notifier to be called once, got %d", notifier.calls)
	}
	if notifier.userID != user.ID() {
		t.Fatalf("notifier userID mismatch: %s", notifier.userID)
	}
	if notifier.email != "u@example.com" {
		t.Fatalf("notifier email mismatch: %s", notifier.email)
	}
}

func TestLogin_Success(t *testing.T) {
	app, repo, _, _ := newTestApp(t)
	ctx := context.Background()

	existing := domain.ReconstituteUser("id-1", "login@example.com", "hash:secret", "active")
	if err := repo.Save(ctx, existing); err != nil {
		t.Fatalf("Save error: %v", err)
	}

	token, _, err := app.Login(ctx, "login@example.com", "secret")
	if err != nil {
		t.Fatalf("Login error: %v", err)
	}
	if token != "test-token" {
		t.Fatalf("token mismatch: %s", token)
	}
}

func TestRegisterUser_JWTSignatureValidation(t *testing.T) {
	app, repo, jwtSvc := newTestAppWithJWT(t, "secret-1")
	ctx := context.Background()

	token, err := app.RegisterUser(ctx, "jwt@example.com", "pass123")
	if err != nil {
		t.Fatalf("RegisterUser error: %v", err)
	}
	user, err := repo.FindByEmail(ctx, "jwt@example.com")
	if err != nil {
		t.Fatalf("FindByEmail error: %v", err)
	}

	userID, err := jwtSvc.ParseToken(token)
	if err != nil {
		t.Fatalf("ParseToken error: %v", err)
	}
	if userID != user.ID() {
		t.Fatalf("userID mismatch: %s", userID)
	}

	otherSvc := infra.NewJWTService("secret-2", "test-issuer")
	if _, err := otherSvc.ParseToken(token); err == nil {
		t.Fatalf("expected signature validation error")
	}
}

func TestRegisterUser_NotificationFailureIsBestEffort(t *testing.T) {
	app, repo, _, notifier := newTestApp(t)
	ctx := context.Background()
	notifier.err = errors.New("message service unavailable")

	token, err := app.RegisterUser(ctx, "best-effort@example.com", "pass123")
	if err != nil {
		t.Fatalf("RegisterUser error: %v", err)
	}
	if token != "test-token" {
		t.Fatalf("token mismatch: %s", token)
	}
	if notifier.calls != 1 {
		t.Fatalf("expected notifier to be called once, got %d", notifier.calls)
	}
	if _, err := repo.FindByEmail(ctx, "best-effort@example.com"); err != nil {
		t.Fatalf("FindByEmail error: %v", err)
	}
}
