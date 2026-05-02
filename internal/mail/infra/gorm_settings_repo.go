package infra

import (
	"backend-core/internal/mail/domain"
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type MailSettingsPO struct {
	ID                              string `gorm:"primaryKey;size:32"`
	RegistrationVerificationEnabled bool   `gorm:"column:registration_verification_enabled;default:false"`
	SMTPEnabled                     bool   `gorm:"column:smtp_enabled;default:false"`
	SMTPHost                        string `gorm:"column:smtp_host;size:255"`
	SMTPPort                        int    `gorm:"column:smtp_port;default:587"`
	SMTPUsername                    string `gorm:"column:smtp_username;size:255"`
	SMTPPassword                    string `gorm:"column:smtp_password;type:text"`
	SMTPFromEmail                   string `gorm:"column:smtp_from_email;size:255"`
	SMTPFromName                    string `gorm:"column:smtp_from_name;size:255"`
	SMTPUseTLS                      bool   `gorm:"column:smtp_use_tls;default:false"`
	SMTPUseStartTLS                 bool   `gorm:"column:smtp_use_start_tls;default:true"`
	CreatedAt                       time.Time
	UpdatedAt                       time.Time
}

func (MailSettingsPO) TableName() string { return "mail_settings" }

type GormSettingsRepo struct {
	db *gorm.DB
}

func NewGormSettingsRepo(db *gorm.DB) *GormSettingsRepo {
	return &GormSettingsRepo{db: db}
}

func (r *GormSettingsRepo) Get(ctx context.Context) (*domain.Settings, error) {
	var po MailSettingsPO
	err := r.db.WithContext(ctx).First(&po, "id = ?", domain.DefaultSettingsID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return domain.DefaultSettings(), nil
		}
		return nil, err
	}
	return poToSettings(&po), nil
}

func (r *GormSettingsRepo) Save(ctx context.Context, settings *domain.Settings) error {
	if settings == nil {
		return errors.New("mail settings is nil")
	}
	now := time.Now()
	po := settingsToPO(settings)
	po.ID = domain.DefaultSettingsID
	po.UpdatedAt = now
	if po.CreatedAt.IsZero() {
		po.CreatedAt = now
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		UpdateAll: true,
	}).Create(po).Error
}

func poToSettings(po *MailSettingsPO) *domain.Settings {
	return &domain.Settings{
		RegistrationVerificationEnabled: po.RegistrationVerificationEnabled,
		SMTP: domain.SMTPSettings{
			Enabled:     po.SMTPEnabled,
			Host:        po.SMTPHost,
			Port:        po.SMTPPort,
			Username:    po.SMTPUsername,
			Password:    po.SMTPPassword,
			FromEmail:   po.SMTPFromEmail,
			FromName:    po.SMTPFromName,
			UseTLS:      po.SMTPUseTLS,
			UseStartTLS: po.SMTPUseStartTLS,
		},
	}
}

func settingsToPO(settings *domain.Settings) *MailSettingsPO {
	return &MailSettingsPO{
		ID:                              domain.DefaultSettingsID,
		RegistrationVerificationEnabled: settings.RegistrationVerificationEnabled,
		SMTPEnabled:                     settings.SMTP.Enabled,
		SMTPHost:                        settings.SMTP.Host,
		SMTPPort:                        settings.SMTP.Port,
		SMTPUsername:                    settings.SMTP.Username,
		SMTPPassword:                    settings.SMTP.Password,
		SMTPFromEmail:                   settings.SMTP.FromEmail,
		SMTPFromName:                    settings.SMTP.FromName,
		SMTPUseTLS:                      settings.SMTP.UseTLS,
		SMTPUseStartTLS:                 settings.SMTP.UseStartTLS,
	}
}
