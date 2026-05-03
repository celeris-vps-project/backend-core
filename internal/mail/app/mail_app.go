package app

import (
	"backend-core/internal/mail/domain"
	"backend-core/pkg/ratelimit"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
)

const verificationCodeTTL = 10 * time.Minute

type Sender interface {
	Send(ctx context.Context, settings domain.SMTPSettings, to, subject, body string) error
}

type SMTPUpdate struct {
	Enabled     bool
	Host        string
	Port        int
	Username    string
	Password    string
	FromEmail   string
	FromName    string
	UseTLS      bool
	UseStartTLS bool
}

type GeneralUpdate struct {
	RegistrationVerificationEnabled bool
	PublicBaseURL                   string
}

type MailAppService struct {
	settingsRepo domain.SettingsRepository
	codeRepo     domain.VerificationCodeRepository
	sender       Sender
	now          func() time.Time
	limiter      *ratelimit.KeyLimiter
}

func NewMailAppService(settingsRepo domain.SettingsRepository, codeRepo domain.VerificationCodeRepository, sender Sender, emailRateLimiter *ratelimit.KeyLimiter) *MailAppService {

	return &MailAppService{
		settingsRepo: settingsRepo,
		codeRepo:     codeRepo,
		sender:       sender,
		now:          time.Now,
		limiter:      emailRateLimiter,
	}
}

func (s *MailAppService) GetSettings(ctx context.Context) (*domain.Settings, error) {
	return s.settingsRepo.Get(ctx)
}

func (s *MailAppService) UpdateGeneral(ctx context.Context, update GeneralUpdate) (*domain.Settings, error) {
	settings, err := s.settingsRepo.Get(ctx)
	if err != nil {
		return nil, err
	}
	publicBaseURL, err := domain.NormalizePublicBaseURL(update.PublicBaseURL)
	if err != nil {
		return nil, err
	}
	settings.RegistrationVerificationEnabled = update.RegistrationVerificationEnabled
	settings.PublicBaseURL = publicBaseURL
	if err := s.settingsRepo.Save(ctx, settings); err != nil {
		return nil, err
	}
	return settings, nil
}

func (s *MailAppService) EnsurePublicBaseURL(ctx context.Context, raw string) error {
	publicBaseURL, err := domain.NormalizePublicBaseURL(raw)
	if err != nil || publicBaseURL == "" {
		return err
	}
	settings, err := s.settingsRepo.Get(ctx)
	if err != nil {
		return err
	}
	if strings.TrimSpace(settings.PublicBaseURL) != "" {
		return nil
	}
	settings.PublicBaseURL = publicBaseURL
	return s.settingsRepo.Save(ctx, settings)
}

func (s *MailAppService) UpdateSMTP(ctx context.Context, update SMTPUpdate) (*domain.Settings, error) {
	settings, err := s.settingsRepo.Get(ctx)
	if err != nil {
		return nil, err
	}

	prevPassword := settings.SMTP.Password
	smtp := domain.SMTPSettings{
		Enabled:     update.Enabled,
		Host:        strings.TrimSpace(update.Host),
		Port:        update.Port,
		Username:    strings.TrimSpace(update.Username),
		Password:    update.Password,
		FromEmail:   strings.TrimSpace(update.FromEmail),
		FromName:    strings.TrimSpace(update.FromName),
		UseTLS:      update.UseTLS,
		UseStartTLS: update.UseStartTLS,
	}
	if smtp.Port == 0 {
		smtp.Port = 587
	}
	if smtp.FromName == "" {
		smtp.FromName = "Celeris"
	}
	if smtp.Password == "" {
		smtp.Password = prevPassword
	}
	if smtp.UseTLS {
		smtp.UseStartTLS = false
	}
	if err := smtp.Validate(); err != nil {
		return nil, err
	}

	settings.SMTP = smtp
	if err := s.settingsRepo.Save(ctx, settings); err != nil {
		return nil, err
	}
	return settings, nil
}

func (s *MailAppService) IsRegistrationVerificationRequired(ctx context.Context) (bool, error) {
	settings, err := s.settingsRepo.Get(ctx)
	if err != nil {
		return false, err
	}
	return settings.RegistrationVerificationRequired(), nil
}

func (s *MailAppService) SendRegistrationCode(ctx context.Context, email string) error {
	return s.sendCode(ctx, email, domain.PurposeRegister, "Celeris registration verification code")
}

func (s *MailAppService) SendPasswordResetCode(ctx context.Context, email string) error {
	return s.sendCode(ctx, email, domain.PurposePasswordReset, "Celeris password reset code")
}

func (s *MailAppService) VerifyRegistrationCode(ctx context.Context, email, code string) error {
	return s.Verify(ctx, email, domain.PurposeRegister, code)
}

func (s *MailAppService) VerifyPasswordResetCode(ctx context.Context, email, code string) error {
	return s.Verify(ctx, email, domain.PurposePasswordReset, code)
}

func (s *MailAppService) TestSMTP(ctx context.Context, to string) error {
	settings, err := s.settingsRepo.Get(ctx)
	if err != nil {
		return err
	}
	if !settings.SMTP.Enabled {
		return domain.ErrSMTPNotEnabled
	}
	email, err := normalizeEmail(to)
	if err != nil {
		return err
	}
	body := "This is a Celeris SMTP test email. If you received it, SMTP delivery is working."
	if err := s.sender.Send(ctx, settings.SMTP, email, "Celeris SMTP test", body); err != nil {
		return fmt.Errorf("%w: %v", domain.ErrMailSendFailed, err)
	}
	return nil
}

func (s *MailAppService) Verify(ctx context.Context, email, purpose, code string) error {
	email, err := normalizeEmail(email)
	if err != nil {
		return err
	}
	code = strings.TrimSpace(code)
	if code == "" {
		return domain.ErrVerificationRequired
	}

	now := s.now()
	found, err := s.codeRepo.FindLatestValid(ctx, email, purpose, now)
	if err != nil {
		return err
	}
	if found.CodeHash != hashCode(email, purpose, code) {
		return domain.ErrInvalidVerificationCode
	}
	return s.codeRepo.MarkUsed(ctx, found.ID, now)
}

func (s *MailAppService) sendCode(ctx context.Context, email, purpose, subject string) error {
	settings, err := s.settingsRepo.Get(ctx)
	if err != nil {
		return err
	}
	if !settings.SMTP.Enabled {
		return domain.ErrSMTPNotEnabled
	}
	if settings.SMTP.FromEmail == "" {
		return errorsAsMailSend("smtp from email is required")
	}

	email, err = normalizeEmail(email)
	if err != nil {
		return err
	}
	if !s.limiter.Allow(purpose + ":" + email) {
		return errorsAsMailSend("only one code can be sent per minute")
	}
	code, err := generateCode()
	if err != nil {
		return err
	}

	now := s.now()
	record := &domain.VerificationCode{
		ID:        uuid.New().String(),
		Email:     email,
		Purpose:   purpose,
		Plain:     code,
		CodeHash:  hashCode(email, purpose, code),
		ExpiresAt: now.Add(verificationCodeTTL),
		CreatedAt: now,
	}
	if err := s.codeRepo.Create(ctx, record); err != nil {
		return err
	}
	body := fmt.Sprintf("Your Celeris verification code is %s. It expires in 10 minutes.", code)
	if err := s.sender.Send(ctx, settings.SMTP, email, subject, body); err != nil {
		return fmt.Errorf("%w: %v", domain.ErrMailSendFailed, err)
	}
	return nil
}

func normalizeEmail(email string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if _, err := mail.ParseAddress(email); err != nil {
		return "", err
	}
	return email, nil
}

func generateCode() (string, error) {
	max := big.NewInt(1000000)
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

func hashCode(email, purpose, code string) string {
	sum := sha256.Sum256([]byte(email + "|" + purpose + "|" + code))
	return hex.EncodeToString(sum[:])
}

func errorsAsMailSend(msg string) error {
	return fmt.Errorf("%w: %s", domain.ErrMailSendFailed, msg)
}
