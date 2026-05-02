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

type TokenGenerator interface {
	Generate(user *domain.User) (string, error)
	ParseToken(tokenString string) (string, error)
}

type PasswordHasher interface {
	Compare(plain, hash string) bool
	Hash(plain string) (string, error)
}

type RegistrationNotifier interface {
	NotifyUserRegistered(ctx context.Context, userID, email string) error
}

type RegistrationVerifier interface {
	IsRegistrationVerificationRequired(ctx context.Context) (bool, error)
	VerifyRegistrationCode(ctx context.Context, email, code string) error
}

type PasswordResetVerifier interface {
	VerifyPasswordResetCode(ctx context.Context, email, code string) error
}

type AuthAppService struct {
	repo                  domain.UserRepository
	token                 TokenGenerator
	hasher                PasswordHasher
	registrationNotifier  RegistrationNotifier
	registrationVerifier  RegistrationVerifier
	passwordResetVerifier PasswordResetVerifier
}

func NewAuthAppService(r domain.UserRepository, t TokenGenerator, h PasswordHasher, registrationNotifier RegistrationNotifier) *AuthAppService {
	return &AuthAppService{
		repo:                 r,
		token:                t,
		hasher:               h,
		registrationNotifier: registrationNotifier,
	}
}

func (app *AuthAppService) SetRegistrationVerifier(verifier RegistrationVerifier) {
	app.registrationVerifier = verifier
}

func (app *AuthAppService) SetPasswordResetVerifier(verifier PasswordResetVerifier) {
	app.passwordResetVerifier = verifier
}

func (app *AuthAppService) Login(ctx context.Context, email, plainPassword string) (string, string, error) {
	user, err := app.repo.FindByEmail(ctx, email)
	if err != nil {
		return "", "", err
	}

	if err := user.Authenticate(plainPassword, app.hasher.Compare); err != nil {
		return "", "", err
	}

	token, err := app.token.Generate(user)
	if err != nil {
		return "", "", err
	}
	return token, user.Role(), nil
}

func (app *AuthAppService) RegisterUser(ctx context.Context, email, plainPassword string, verificationCode ...string) (string, error) {
	if _, err := app.repo.FindByEmail(ctx, email); err == nil {
		return "", errors.New("email already registered")
	}

	if app.registrationVerifier != nil {
		required, err := app.registrationVerifier.IsRegistrationVerificationRequired(ctx)
		if err != nil {
			return "", err
		}
		if required {
			code := ""
			if len(verificationCode) > 0 {
				code = verificationCode[0]
			}
			if err := app.registrationVerifier.VerifyRegistrationCode(ctx, email, code); err != nil {
				return "", err
			}
		}
	}

	hash, err := app.hasher.Hash(plainPassword)
	if err != nil {
		return "", err
	}

	newUser := domain.ReconstituteUser(uuid.New().String(), email, hash, "active")
	if err := app.repo.Save(ctx, newUser); err != nil {
		return "", err
	}

	token, err := app.token.Generate(newUser)
	if err != nil {
		return "", err
	}

	if app.registrationNotifier != nil {
		if err := app.registrationNotifier.NotifyUserRegistered(ctx, newUser.ID(), newUser.Email()); err != nil {
			log.Printf("[identity] failed to publish user_registered notification for user %s: %v", newUser.ID(), err)
		}
	}

	return token, nil
}

func (app *AuthAppService) ResetPassword(ctx context.Context, email, code, newPassword string) error {
	if app.passwordResetVerifier == nil {
		return errors.New("password reset is not configured")
	}
	user, err := app.repo.FindByEmail(ctx, email)
	if err != nil {
		return err
	}
	if err := app.passwordResetVerifier.VerifyPasswordResetCode(ctx, email, code); err != nil {
		return err
	}
	newHash, err := app.hasher.Hash(newPassword)
	if err != nil {
		return err
	}
	return app.repo.UpdatePasswordHash(ctx, user.ID(), newHash)
}

func (app *AuthAppService) ChangePassword(ctx context.Context, userID, oldPassword, newPassword string) error {
	user, err := app.repo.FindByID(ctx, userID)
	if err != nil {
		return errors.New("user not found")
	}

	if !app.hasher.Compare(oldPassword, user.PasswordHash()) {
		return errors.New("old password is incorrect")
	}

	newHash, err := app.hasher.Hash(newPassword)
	if err != nil {
		return err
	}

	return app.repo.UpdatePasswordHash(ctx, userID, newHash)
}

func (app *AuthAppService) EnsureAdmin(ctx context.Context, email string) {
	if email == "" {
		email = "admin@celeris.local"
	}

	if _, err := app.repo.FindByEmail(ctx, email); err == nil {
		log.Printf("[api] admin account already exists (%s), skipping seed", email)
		return
	}

	password := generateRandomPassword(12)
	hash, err := app.hasher.Hash(password)
	if err != nil {
		log.Printf("[api] FATAL: failed to hash admin password: %v", err)
		return
	}

	adminUser := domain.ReconstituteUserWithRole(uuid.New().String(), email, hash, "active", domain.RoleAdmin)
	if err := app.repo.Save(ctx, adminUser); err != nil {
		log.Printf("[api] FATAL: failed to create admin account: %v", err)
		return
	}

	log.Printf("[api] ========================================")
	log.Printf("[api]  ADMIN ACCOUNT CREATED")
	log.Printf("[api]  Email:    %s", email)
	log.Printf("[api]  Password: %s", password)
	log.Printf("[api]  This password will NOT be shown again.")
	log.Printf("[api] ========================================")
}

func generateRandomPassword(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%"
	result := make([]byte, length)
	for i := range result {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			result[i] = charset[i%len(charset)]
			continue
		}
		result[i] = charset[n.Int64()]
	}
	return string(result)
}
