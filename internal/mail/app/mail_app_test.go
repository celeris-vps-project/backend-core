package app

import (
	"backend-core/internal/mail/domain"
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	mailInfra "backend-core/internal/mail/infra"
)

type fakeSender struct {
	calls    int
	lastTo   string
	lastBody string
	err      error
}

func (s *fakeSender) Send(ctx context.Context, settings domain.SMTPSettings, to, subject, body string) error {
	s.calls++
	s.lastTo = to
	s.lastBody = body
	return s.err
}

func newMailTestApp(t *testing.T) (*MailAppService, *fakeSender) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&mailInfra.MailSettingsPO{}, &mailInfra.MailVerificationCodePO{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	sender := &fakeSender{}
	return NewMailAppService(
		mailInfra.NewGormSettingsRepo(db),
		mailInfra.NewGormVerificationCodeRepo(db),
		sender,
	), sender
}

func TestRegistrationVerificationRequiredNeedsSMTPEnabled(t *testing.T) {
	svc, _ := newMailTestApp(t)
	ctx := context.Background()

	if _, err := svc.UpdateGeneral(ctx, GeneralUpdate{RegistrationVerificationEnabled: true}); err != nil {
		t.Fatalf("UpdateGeneral: %v", err)
	}
	required, err := svc.IsRegistrationVerificationRequired(ctx)
	if err != nil {
		t.Fatalf("IsRegistrationVerificationRequired: %v", err)
	}
	if required {
		t.Fatalf("verification should not be required until SMTP is enabled")
	}

	if _, err := svc.UpdateSMTP(ctx, SMTPUpdate{
		Enabled:     true,
		Host:        "smtp.example.com",
		Port:        587,
		FromEmail:   "noreply@example.com",
		FromName:    "Celeris",
		UseStartTLS: true,
	}); err != nil {
		t.Fatalf("UpdateSMTP: %v", err)
	}
	required, err = svc.IsRegistrationVerificationRequired(ctx)
	if err != nil {
		t.Fatalf("IsRegistrationVerificationRequired: %v", err)
	}
	if !required {
		t.Fatalf("verification should be required when global flag and SMTP are enabled")
	}
}

func TestSendRegistrationCodeRequiresSMTP(t *testing.T) {
	svc, _ := newMailTestApp(t)
	err := svc.SendRegistrationCode(context.Background(), "u@example.com")
	if err != domain.ErrSMTPNotEnabled {
		t.Fatalf("expected ErrSMTPNotEnabled, got %v", err)
	}
}
