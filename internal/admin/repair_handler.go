package admin

import (
	"backend-core/pkg/apperr"
	"context"
	"strings"

	hz_app "github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

type RepairHandler struct {
	svc *RepairService
}

func NewRepairHandler(svc *RepairService) *RepairHandler {
	return &RepairHandler{svc: svc}
}

func (h *RepairHandler) ListProvisioning(ctx context.Context, c *hz_app.RequestContext) {
	items, err := h.svc.ListProvisioningRepairs(ctx)
	if err != nil {
		c.JSON(consts.StatusInternalServerError, apperr.Resp(apperr.CodeInternalError, err.Error()))
		return
	}
	c.JSON(consts.StatusOK, utils.H{"data": items})
}

func (h *RepairHandler) RepairProvisioning(ctx context.Context, c *hz_app.RequestContext) {
	orderID := strings.TrimSpace(c.Param("orderId"))
	result, err := h.svc.RepairProvisioning(ctx, orderID)
	if err != nil {
		c.JSON(consts.StatusUnprocessableEntity, apperr.Resp(classifyRepairError(err), err.Error()))
		return
	}
	status := consts.StatusOK
	if result != nil && result.Queued {
		status = consts.StatusAccepted
	}
	c.JSON(status, utils.H{"data": result})
}

func classifyRepairError(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "not found"):
		return apperr.CodeOrderNotFound
	case strings.Contains(msg, "no available physical slots"), strings.Contains(msg, "did not queue"):
		return apperr.CodeNoAvailableSlots
	case strings.Contains(msg, "active provisioning task"):
		return apperr.CodeSlotConflict
	default:
		return apperr.CodeInvalidStateTransition
	}
}
