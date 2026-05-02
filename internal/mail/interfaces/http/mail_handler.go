package http

import (
	"context"
	"errors"
	"net/mail"

	identityApp "backend-core/internal/identity/app"
	mailApp "backend-core/internal/mail/app"
	"backend-core/internal/mail/domain"
	"backend-core/pkg/apperr"

	hz_app "github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

type MailHandler struct {
	mailApp *mailApp.MailAppService
	authApp *identityApp.AuthAppService
}

func NewMailHandler(mailApp *mailApp.MailAppService, authApp *identityApp.AuthAppService) *MailHandler {
	return &MailHandler{mailApp: mailApp, authApp: authApp}
}

type publicOptionsResponse struct {
	RegistrationVerificationRequired bool `json:"registration_verification_required"`
	SMTPEnabled                      bool `json:"smtp_enabled"`
}

func (h *MailHandler) PublicOptions(ctx context.Context, c *hz_app.RequestContext) {
	settings, err := h.mailApp.GetSettings(ctx)
	if err != nil {
		c.JSON(consts.StatusInternalServerError, apperr.Resp(apperr.CodeInternalError, err.Error()))
		return
	}
	c.JSON(consts.StatusOK, utils.H{"data": publicOptionsResponse{
		RegistrationVerificationRequired: settings.RegistrationVerificationRequired(),
		SMTPEnabled:                      settings.SMTP.Enabled,
	}})
}

type emailRequest struct {
	Email string `json:"email"`
}

func (h *MailHandler) SendRegistrationCode(ctx context.Context, c *hz_app.RequestContext) {
	var req emailRequest
	if err := c.BindJSON(&req); err != nil {
		c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeInvalidParams, "invalid request body"))
		return
	}
	if err := h.mailApp.SendRegistrationCode(ctx, req.Email); err != nil {
		writeMailError(c, err)
		return
	}
	c.JSON(consts.StatusOK, utils.H{"message": "verification code sent"})
}

func (h *MailHandler) SendPasswordResetCode(ctx context.Context, c *hz_app.RequestContext) {
	var req emailRequest
	if err := c.BindJSON(&req); err != nil {
		c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeInvalidParams, "invalid request body"))
		return
	}
	if err := h.mailApp.SendPasswordResetCode(ctx, req.Email); err != nil {
		writeMailError(c, err)
		return
	}
	c.JSON(consts.StatusOK, utils.H{"message": "password reset code sent"})
}

type resetPasswordRequest struct {
	Email       string `json:"email"`
	Code        string `json:"code"`
	NewPassword string `json:"new_password"`
}

func (h *MailHandler) ResetPassword(ctx context.Context, c *hz_app.RequestContext) {
	var req resetPasswordRequest
	if err := c.BindJSON(&req); err != nil {
		c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeInvalidParams, "invalid request body"))
		return
	}
	if req.NewPassword == "" {
		c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeInvalidParams, "new password is required"))
		return
	}
	if err := h.authApp.ResetPassword(ctx, req.Email, req.Code, req.NewPassword); err != nil {
		writeMailError(c, err)
		return
	}
	c.JSON(consts.StatusOK, utils.H{"message": "password reset successful"})
}

type generalSettingsResponse struct {
	RegistrationVerificationEnabled  bool `json:"registration_verification_enabled"`
	RegistrationVerificationRequired bool `json:"registration_verification_required"`
	SMTPEnabled                      bool `json:"smtp_enabled"`
}

func (h *MailHandler) GetGeneral(ctx context.Context, c *hz_app.RequestContext) {
	settings, err := h.mailApp.GetSettings(ctx)
	if err != nil {
		c.JSON(consts.StatusInternalServerError, apperr.Resp(apperr.CodeInternalError, err.Error()))
		return
	}
	c.JSON(consts.StatusOK, utils.H{"data": generalSettingsResponse{
		RegistrationVerificationEnabled:  settings.RegistrationVerificationEnabled,
		RegistrationVerificationRequired: settings.RegistrationVerificationRequired(),
		SMTPEnabled:                      settings.SMTP.Enabled,
	}})
}

type updateGeneralRequest struct {
	RegistrationVerificationEnabled bool `json:"registration_verification_enabled"`
}

func (h *MailHandler) UpdateGeneral(ctx context.Context, c *hz_app.RequestContext) {
	var req updateGeneralRequest
	if err := c.BindJSON(&req); err != nil {
		c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeInvalidParams, "invalid request body"))
		return
	}
	settings, err := h.mailApp.UpdateGeneral(ctx, req.RegistrationVerificationEnabled)
	if err != nil {
		writeMailError(c, err)
		return
	}
	c.JSON(consts.StatusOK, utils.H{"data": generalSettingsResponse{
		RegistrationVerificationEnabled:  settings.RegistrationVerificationEnabled,
		RegistrationVerificationRequired: settings.RegistrationVerificationRequired(),
		SMTPEnabled:                      settings.SMTP.Enabled,
	}})
}

type smtpSettingsResponse struct {
	Enabled     bool   `json:"enabled"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Username    string `json:"username"`
	PasswordSet bool   `json:"password_set"`
	FromEmail   string `json:"from_email"`
	FromName    string `json:"from_name"`
	UseTLS      bool   `json:"use_tls"`
	UseStartTLS bool   `json:"use_starttls"`
}

func (h *MailHandler) GetSMTP(ctx context.Context, c *hz_app.RequestContext) {
	settings, err := h.mailApp.GetSettings(ctx)
	if err != nil {
		c.JSON(consts.StatusInternalServerError, apperr.Resp(apperr.CodeInternalError, err.Error()))
		return
	}
	c.JSON(consts.StatusOK, utils.H{"data": smtpResponse(settings.SMTP)})
}

type updateSMTPRequest struct {
	Enabled     bool   `json:"enabled"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	FromEmail   string `json:"from_email"`
	FromName    string `json:"from_name"`
	UseTLS      bool   `json:"use_tls"`
	UseStartTLS bool   `json:"use_starttls"`
}

func (h *MailHandler) UpdateSMTP(ctx context.Context, c *hz_app.RequestContext) {
	var req updateSMTPRequest
	if err := c.BindJSON(&req); err != nil {
		c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeInvalidParams, "invalid request body"))
		return
	}
	settings, err := h.mailApp.UpdateSMTP(ctx, mailApp.SMTPUpdate{
		Enabled:     req.Enabled,
		Host:        req.Host,
		Port:        req.Port,
		Username:    req.Username,
		Password:    req.Password,
		FromEmail:   req.FromEmail,
		FromName:    req.FromName,
		UseTLS:      req.UseTLS,
		UseStartTLS: req.UseStartTLS,
	})
	if err != nil {
		writeMailError(c, err)
		return
	}
	c.JSON(consts.StatusOK, utils.H{"data": smtpResponse(settings.SMTP)})
}

func (h *MailHandler) TestSMTP(ctx context.Context, c *hz_app.RequestContext) {
	var req emailRequest
	if err := c.BindJSON(&req); err != nil {
		c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeInvalidParams, "invalid request body"))
		return
	}
	if err := h.mailApp.TestSMTP(ctx, req.Email); err != nil {
		writeMailError(c, err)
		return
	}
	c.JSON(consts.StatusOK, utils.H{"message": "test email sent"})
}

func smtpResponse(settings domain.SMTPSettings) smtpSettingsResponse {
	return smtpSettingsResponse{
		Enabled:     settings.Enabled,
		Host:        settings.Host,
		Port:        settings.Port,
		Username:    settings.Username,
		PasswordSet: settings.Password != "",
		FromEmail:   settings.FromEmail,
		FromName:    settings.FromName,
		UseTLS:      settings.UseTLS,
		UseStartTLS: settings.UseStartTLS,
	}
}

func writeMailError(c *hz_app.RequestContext, err error) {
	switch {
	case errors.Is(err, domain.ErrSMTPNotEnabled):
		c.JSON(consts.StatusUnprocessableEntity, apperr.Resp(apperr.CodeSMTPNotEnabled, err.Error()))
	case errors.Is(err, domain.ErrVerificationRequired):
		c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeVerificationRequired, err.Error()))
	case errors.Is(err, domain.ErrInvalidVerificationCode):
		c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeInvalidVerificationCode, err.Error()))
	case errors.Is(err, domain.ErrMailSendFailed):
		c.JSON(consts.StatusBadGateway, apperr.Resp(apperr.CodeMailSendFailed, err.Error()))
	default:
		if _, parseErr := mail.ParseAddress(err.Error()); parseErr == nil {
			c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeInvalidParams, err.Error()))
			return
		}
		c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeInvalidParams, err.Error()))
	}
}
