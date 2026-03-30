package infra

import (
	instanceApp "backend-core/internal/instance/app"
	paymentApp "backend-core/internal/payment/app"
)

// InstanceAdapter implements paymentApp.InstanceCreator by wrapping the
// instance context's InstanceAppService. It returns only the instance ID
// string so the payment context never imports instance domain types directly.
type InstanceAdapter struct {
	svc *instanceApp.InstanceAppService
}

// NewInstanceAdapter wraps an InstanceAppService as an InstanceCreator.
func NewInstanceAdapter(svc *instanceApp.InstanceAppService) *InstanceAdapter {
	return &InstanceAdapter{svc: svc}
}

// CreatePendingInstance delegates to the instance app service and returns
// the new instance ID.
func (a *InstanceAdapter) CreatePendingInstance(
	customerID, orderID, region, hostname, plan, os string,
	cpu, memoryMB, diskGB int,
) (string, error) {
	inst, err := a.svc.CreatePendingInstance(customerID, orderID, region, hostname, plan, os, cpu, memoryMB, diskGB)
	if err != nil {
		return "", err
	}
	return inst.ID(), nil
}

func (a *InstanceAdapter) GetByOrderID(orderID string) (paymentApp.RenewalInstance, error) {
	inst, err := a.svc.GetByOrderID(orderID)
	if err != nil {
		return paymentApp.RenewalInstance{}, err
	}
	return paymentApp.RenewalInstance{
		ID:     inst.ID(),
		Status: inst.Status(),
	}, nil
}

func (a *InstanceAdapter) SuspendInstance(instanceID string) error {
	return a.svc.SuspendInstance(instanceID)
}

func (a *InstanceAdapter) RecoverFromBillingSuspension(instanceID string) error {
	return a.svc.RecoverFromBillingSuspension(instanceID)
}
