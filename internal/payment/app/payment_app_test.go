package app

import (
	"backend-core/internal/payment/domain"
	"backend-core/internal/payment/infra"
	"testing"
	"time"
)

func TestPaymentAppService_Process_MockProvider(t *testing.T) {
	var received *domain.WebhookPayload

	mock := infra.NewMockPaymentProvider(func(payload *domain.WebhookPayload) {
		received = payload
	})

	svc := NewPaymentAppService(mock)

	result, err := svc.Process("order-123", "USD", 9900)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Mock now returns "pending" (simulating real gateway flow)
	if result.Status != domain.ChargeStatusPending {
		t.Fatalf("expected status %s, got %s", domain.ChargeStatusPending, result.Status)
	}
	if result.ChargeID == "" {
		t.Fatal("expected a non-empty charge id")
	}

	// Webhook fires asynchronously after ~2s delay — wait for it
	time.Sleep(3 * time.Second)

	if received == nil {
		t.Fatal("expected webhook callback to be triggered")
	}
	if received.OrderID != "order-123" {
		t.Fatalf("webhook order id mismatch: got %s", received.OrderID)
	}
	if received.Status != domain.ChargeStatusSuccess {
		t.Fatalf("webhook status mismatch: got %s", received.Status)
	}
}

func TestPaymentAppService_Process_ValidationErrors(t *testing.T) {
	mock := infra.NewMockPaymentProvider(nil)
	svc := NewPaymentAppService(mock)

	if _, err := svc.Process("", "USD", 100); err == nil {
		t.Fatal("expected error for empty order id")
	}
	if _, err := svc.Process("order-1", "USD", 0); err == nil {
		t.Fatal("expected error for zero amount")
	}
	if _, err := svc.Process("order-1", "USD", -1); err == nil {
		t.Fatal("expected error for negative amount")
	}
}
