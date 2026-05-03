package domain

import (
	"errors"
	"net/mail"
	"net/url"
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
	PublicBaseURL                   string
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

func NormalizePublicBaseURL(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", nil
	}
	if !strings.Contains(value, "://") {
		value = "https://" + value
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("public base url is invalid")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("public base url scheme must be http or https")
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/"), nil
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
