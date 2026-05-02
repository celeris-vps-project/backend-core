package domain

import (
	"errors"
	"net/mail"
	"strings"
)

const (
	DefaultSettingsID = "default"

	PurposeRegister      = "register"
	PurposePasswordReset = "password_reset"
)

var (
	ErrSMTPNotEnabled          = errors.New("smtp is not enabled")
	ErrVerificationRequired    = errors.New("verification code is required")
	ErrInvalidVerificationCode = errors.New("invalid verification code")
	ErrMailSendFailed          = errors.New("mail send failed")
)

type SMTPSettings struct {
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

type Settings struct {
	RegistrationVerificationEnabled bool
	SMTP                            SMTPSettings
}

func DefaultSettings() *Settings {
	return &Settings{
		RegistrationVerificationEnabled: false,
		SMTP: SMTPSettings{
			Port:        587,
			FromName:    "Celeris",
			UseStartTLS: true,
		},
	}
}

func (s Settings) RegistrationVerificationRequired() bool {
	return s.RegistrationVerificationEnabled && s.SMTP.Enabled
}

func (s SMTPSettings) Validate() error {
	if !s.Enabled {
		return nil
	}
	if strings.TrimSpace(s.Host) == "" {
		return errors.New("smtp host is required")
	}
	if s.Port <= 0 || s.Port > 65535 {
		return errors.New("smtp port must be between 1 and 65535")
	}
	if strings.TrimSpace(s.FromEmail) == "" {
		return errors.New("smtp from email is required")
	}
	if _, err := mail.ParseAddress(s.FromEmail); err != nil {
		return errors.New("smtp from email is invalid")
	}
	return nil
}
