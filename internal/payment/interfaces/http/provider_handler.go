package http

import (
	"context"

	paymentApp "backend-core/internal/payment/app"
	"backend-core/internal/payment/domain"
	paymentInfra "backend-core/internal/payment/infra"
	"backend-core/pkg/apperr"

	hz_app "github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

// ProviderHandler exposes HTTP endpoints for payment provider management (admin)
// and provider listing (public, for user checkout).
type ProviderHandler struct {
	svc *paymentApp.ProviderAppService
}

func NewProviderHandler(svc *paymentApp.ProviderAppService) *ProviderHandler {
	return &ProviderHandler{svc: svc}
}

// ── Admin endpoints ────────────────────────────────────────────────────

// createProviderRequest is the JSON body for POST /admin/payment-providers.
type createProviderRequest struct {
	Type      string                 `json:"type"`
	Name      string                 `json:"name"`
	SortOrder int                    `json:"sort_order"`
	Config    map[string]interface{} `json:"config"`
}

// Create handles POST /api/v1/admin/payment-providers
func (h *ProviderHandler) Create(ctx context.Context, c *hz_app.RequestContext) {
	var req createProviderRequest
	if err := c.BindJSON(&req); err != nil {
		c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeInvalidParams, "invalid request body"))
		return
	}
	if req.Type == "" || req.Name == "" {
		c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeInvalidParams, "type and name are required"))
		return
	}

	p, err := h.svc.CreateProvider(req.Type, req.Name, req.SortOrder, req.Config)
	if err != nil {
		c.JSON(consts.StatusUnprocessableEntity, apperr.Resp(apperr.CodeInternalError, err.Error()))
		return
	}

	// For custom / epay providers, auto-fill the notify_url with the standard
	// webhook callback endpoint so the admin can copy it into the gateway dashboard.
	if p.Type == domain.ProviderTypeCustom || p.Type == domain.ProviderTypeEPay {
		var notifyURL string
		if p.Type == domain.ProviderTypeCustom {
			notifyURL = paymentInfra.BuildCustomNotifyURL(p.ID)
		} else {
			notifyURL = paymentInfra.BuildEPayNotifyURL(p.ID)
		}
		if p.Config == nil {
			p.Config = make(map[string]interface{})
		}
		// Only set if not manually provided
		if existing, _ := p.Config["notify_url"].(string); existing == "" {
			p.Config["notify_url"] = notifyURL
			_, _ = h.svc.UpdateProvider(p.ID, p.Name, p.SortOrder, p.Config)
		}
	}

	c.JSON(consts.StatusCreated, utils.H{"data": p})
}

// ListAll handles GET /api/v1/admin/payment-providers
func (h *ProviderHandler) ListAll(ctx context.Context, c *hz_app.RequestContext) {
	providers, err := h.svc.ListAllProviders()
	if err != nil {
		c.JSON(consts.StatusInternalServerError, apperr.Resp(apperr.CodeInternalError, err.Error()))
		return
	}
	c.JSON(consts.StatusOK, utils.H{"data": providers})
}

// GetByID handles GET /api/v1/admin/payment-providers/:id
func (h *ProviderHandler) GetByID(ctx context.Context, c *hz_app.RequestContext) {
	id := c.Param("id")
	p, err := h.svc.GetProvider(id)
	if err != nil {
		c.JSON(consts.StatusNotFound, apperr.Resp(apperr.CodeProviderNotFound, "provider not found"))
		return
	}
	c.JSON(consts.StatusOK, utils.H{"data": p})
}

// updateProviderRequest is the JSON body for PUT /admin/payment-providers/:id.
type updateProviderRequest struct {
	Name      string                 `json:"name"`
	SortOrder int                    `json:"sort_order"`
	Config    map[string]interface{} `json:"config"`
}

// Update handles PUT /api/v1/admin/payment-providers/:id
func (h *ProviderHandler) Update(ctx context.Context, c *hz_app.RequestContext) {
	id := c.Param("id")
	var req updateProviderRequest
	if err := c.BindJSON(&req); err != nil {
		c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeInvalidParams, "invalid request body"))
		return
	}

	p, err := h.svc.UpdateProvider(id, req.Name, req.SortOrder, req.Config)
	if err != nil {
		c.JSON(consts.StatusNotFound, apperr.Resp(apperr.CodeProviderNotFound, err.Error()))
		return
	}

	c.JSON(consts.StatusOK, utils.H{"data": p})
}

// Enable handles POST /api/v1/admin/payment-providers/:id/enable
func (h *ProviderHandler) Enable(ctx context.Context, c *hz_app.RequestContext) {
	id := c.Param("id")
	if err := h.svc.EnableProvider(id); err != nil {
		c.JSON(consts.StatusNotFound, apperr.Resp(apperr.CodeProviderNotFound, err.Error()))
		return
	}
	c.JSON(consts.StatusOK, utils.H{"message": "provider enabled"})
}

// Disable handles POST /api/v1/admin/payment-providers/:id/disable
func (h *ProviderHandler) Disable(ctx context.Context, c *hz_app.RequestContext) {
	id := c.Param("id")
	if err := h.svc.DisableProvider(id); err != nil {
		c.JSON(consts.StatusNotFound, apperr.Resp(apperr.CodeProviderNotFound, err.Error()))
		return
	}
	c.JSON(consts.StatusOK, utils.H{"message": "provider disabled"})
}

// Delete handles DELETE /api/v1/admin/payment-providers/:id
func (h *ProviderHandler) Delete(ctx context.Context, c *hz_app.RequestContext) {
	id := c.Param("id")
	if err := h.svc.DeleteProvider(id); err != nil {
		c.JSON(consts.StatusNotFound, apperr.Resp(apperr.CodeProviderNotFound, err.Error()))
		return
	}
	c.JSON(consts.StatusOK, utils.H{"message": "provider deleted"})
}

// ProviderTypes handles GET /api/v1/admin/payment-providers/types
// Returns all supported provider types and their config field definitions.
func (h *ProviderHandler) ProviderTypes(ctx context.Context, c *hz_app.RequestContext) {
	types := domain.SupportedProviderTypes()
	c.JSON(consts.StatusOK, utils.H{"data": types})
}

// ── Public endpoints (user-facing) ─────────────────────────────────────

// ListEnabled handles GET /api/v1/payment/providers
// Returns only enabled providers for user checkout selection.
// Config secrets are stripped — only type, name, and ID are returned.
func (h *ProviderHandler) ListEnabled(ctx context.Context, c *hz_app.RequestContext) {
	providers, err := h.svc.ListEnabledProviders()
	if err != nil {
		c.JSON(consts.StatusInternalServerError, apperr.Resp(apperr.CodeInternalError, err.Error()))
		return
	}

	// Strip sensitive config from the response — only expose safe metadata
	type publicProvider struct {
		ID   string `json:"id"`
		Type string `json:"type"`
		Name string `json:"name"`
		// For crypto_usdt, expose the supported networks (not the full wallet addresses)
		Networks []string `json:"networks,omitempty"`
	}

	result := make([]publicProvider, 0, len(providers))
	for _, p := range providers {
		pp := publicProvider{
			ID:   p.ID,
			Type: p.Type,
			Name: p.Name,
		}
		// For crypto providers, extract network names from the wallets config
		if p.Type == domain.ProviderTypeCryptoUSDT {
			if wallets, ok := p.Config["wallets"].(map[string]interface{}); ok {
				for network := range wallets {
					pp.Networks = append(pp.Networks, network)
				}
			}
		}
		result = append(result, pp)
	}

	c.JSON(consts.StatusOK, utils.H{"data": result})
}
