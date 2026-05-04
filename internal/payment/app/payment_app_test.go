package app_test

import (
	"backend-core/internal/payment/app"
	"backend-core/internal/payment/domain"
	"backend-core/pkg/apperr"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"
)

// ── Minimal mock implementations for testing ───────────────────────────

type mockOrderActivator struct {
	order       app.PayableOrder
	orders      []app.PayableOrder
	getErr      error
	activateHit bool
}

func (m *mockOrderActivator) ActivateOrder(orderID string) error {
	m.activateHit = true
	m.order.Status = "active"
	for i := range m.orders {
		if m.orders[i].ID == orderID {
			m.orders[i].Status = "active"
		}
	}
	return nil
}
func (m *mockOrderActivator) GetOrderForPayment(orderID string) (app.PayableOrder, error) {
	return m.order, m.getErr
}
func (m *mockOrderActivator) ListOrders() ([]app.PayableOrder, error) {
	if m.orders != nil {
		return m.orders, nil
	}
	return []app.PayableOrder{m.order}, nil
}
func (m *mockOrderActivator) LinkInvoiceToOrder(orderID, invoiceID string) error {
	m.order.InvoiceID = invoiceID
	for i := range m.orders {
		if m.orders[i].ID == orderID {
			m.orders[i].InvoiceID = invoiceID
		}
	}
	return nil
}
func (m *mockOrderActivator) CancelOrder(orderID, reason string) error { return nil }

type mockProductPurchaser struct {
	networkMode string
	reserveHits int
	releaseHits int
	reserveErr  error
}

func (m *mockProductPurchaser) PurchaseProduct(ctx context.Context, productID, customerID, orderID, instanceID, initialPassword, hostname, os, networkMode string) (app.PurchasedProduct, error) {
	m.networkMode = networkMode
	return app.PurchasedProduct{}, nil
}

func (m *mockProductPurchaser) ReserveProduct(ctx context.Context, productID string) error {
	m.reserveHits++
	return m.reserveErr
}

func (m *mockProductPurchaser) ReleaseProduct(ctx context.Context, productID string) error {
	m.releaseHits++
	return nil
}

type mockInstanceCreator struct {
	networkMode string
}

func (m *mockInstanceCreator) CreatePendingInstance(customerID, orderID, region, hostname, plan, os, networkMode string, cpu, memoryMB, diskGB int) (app.PendingInstance, error) {
	m.networkMode = networkMode
	return app.PendingInstance{ID: "inst-1", InitialPassword: "pwd-1"}, nil
}

type mockInvoiceCreator struct {
	createCount int
}

func (m *mockInvoiceCreator) CreateAndIssueInvoice(customerID, currency, billingCycle, description string, priceAmount int64) (string, error) {
	m.createCount++
	return fmt.Sprintf("inv-%d", m.createCount), nil
}
func (m *mockInvoiceCreator) RecordInvoicePayment(invoiceID string, amount int64, currency string) error {
	return nil
}
func (m *mockInvoiceCreator) VoidInvoice(invoiceID, reason string) error { return nil }
func (m *mockInvoiceCreator) GetInvoiceStatus(invoiceID string) (string, error) {
	return "issued", nil
}
func (m *mockInvoiceCreator) GetInvoiceForPayment(invoiceID string) (app.PayableInvoice, error) {
	return app.PayableInvoice{
		ID:       invoiceID,
		Status:   "issued",
		Currency: "USD",
		Total:    7,
	}, nil
}

type mockCouponApplier struct {
	code       string
	amountSeen int64
}

func (m *mockCouponApplier) ApplyCoupon(_ context.Context, req app.CouponApplicationRequest) (app.CouponApplicationResult, error) {
	m.code = req.Code
	m.amountSeen = req.OriginalAmount
	return app.CouponApplicationResult{
		Applied:        req.Code != "",
		CouponID:       "coupon-1",
		Code:           req.Code,
		DiscountAmount: req.OriginalAmount,
		FinalAmount:    0,
	}, nil
}

func TestInitiatePayment_ZeroCouponConfirmsImmediately(t *testing.T) {
	order := app.PayableOrder{
		ID:          "order-1",
		Status:      "pending",
		ProductID:   "prod-1",
		CustomerID:  "cust-1",
		Currency:    "USD",
		PriceAmount: 100,
	}
	orch := app.NewPostPaymentOrchestrator(
		&mockOrderActivator{order: order},
		&mockProductPurchaser{},
		nil,
		&mockInvoiceCreator{},
		nil,
	)
	coupon := &mockCouponApplier{}
	svc := app.NewPaymentAppService(nil, orch, nil)
	svc.SetCouponApplier(coupon)

	resp, err := svc.InitiatePayment(context.Background(), &app.InitiatePaymentRequest{
		OrderID:    "order-1",
		CouponCode: "FREE100",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if coupon.code != "FREE100" {
		t.Fatalf("expected coupon code FREE100, got %s", coupon.code)
	}
	if coupon.amountSeen != 100 {
		t.Fatalf("expected original amount 100, got %d", coupon.amountSeen)
	}
	if resp.PayableAmount != 0 {
		t.Fatalf("expected payable amount 0, got %d", resp.PayableAmount)
	}
	if resp.Status != domain.ChargeStatusSuccess {
		t.Fatalf("expected success status, got %s", resp.Status)
	}
	if resp.ChargeID != "coupon:coupon-1" {
		t.Fatalf("expected coupon charge id, got %s", resp.ChargeID)
	}
}

func TestInitiatePayment_ReservesProductBeforeCharge(t *testing.T) {
	order := app.PayableOrder{
		ID:          "order-1",
		Status:      "pending",
		CustomerID:  "cust-1",
		ProductID:   "prod-1",
		Currency:    "USD",
		PriceAmount: 7,
	}
	products := &mockProductPurchaser{}
	orch := app.NewPostPaymentOrchestrator(
		&mockOrderActivator{order: order},
		products,
		nil,
		&mockInvoiceCreator{},
		nil,
	)
	providerSvc := newEPayProviderServiceForTest("provider-1", "merch_1", "secret")
	attempts := &memoryAttemptRepo{}
	svc := app.NewPaymentAppService(providerSvc, orch, nil)
	svc.SetPaymentAttemptStore(attempts, noOpIDGen{})

	_, err := svc.InitiatePayment(context.Background(), &app.InitiatePaymentRequest{
		OrderID:    "order-1",
		ProviderID: "provider-1",
		PayType:    "alipay",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if products.reserveHits != 1 {
		t.Fatalf("expected one product reservation, got %d", products.reserveHits)
	}
	if products.releaseHits != 0 {
		t.Fatalf("expected no release on successful charge creation, got %d", products.releaseHits)
	}
}

func TestInitiatePayment_ReleasesProductWhenChargeCreationFails(t *testing.T) {
	order := app.PayableOrder{
		ID:          "order-1",
		Status:      "pending",
		CustomerID:  "cust-1",
		ProductID:   "prod-1",
		Currency:    "USD",
		PriceAmount: 7,
	}
	products := &mockProductPurchaser{}
	orch := app.NewPostPaymentOrchestrator(
		&mockOrderActivator{order: order},
		products,
		nil,
		&mockInvoiceCreator{},
		nil,
	)
	svc := app.NewPaymentAppService(nil, orch, nil)

	_, err := svc.InitiatePayment(context.Background(), &app.InitiatePaymentRequest{OrderID: "order-1"})
	if err == nil {
		t.Fatal("expected charge creation error")
	}
	if products.reserveHits != 1 {
		t.Fatalf("expected one product reservation, got %d", products.reserveHits)
	}
	if products.releaseHits != 1 {
		t.Fatalf("expected reserved product to be released once, got %d", products.releaseHits)
	}
}

type stubProviderRepo struct {
	provider *domain.PaymentProviderConfig
}

func (r *stubProviderRepo) Create(p *domain.PaymentProviderConfig) error {
	r.provider = p
	return nil
}

func (r *stubProviderRepo) GetByID(id string) (*domain.PaymentProviderConfig, error) {
	if r.provider == nil || r.provider.ID != id {
		return nil, errors.New("provider not found")
	}
	cp := *r.provider
	if r.provider.Config != nil {
		cp.Config = make(map[string]interface{}, len(r.provider.Config))
		for k, v := range r.provider.Config {
			cp.Config[k] = v
		}
	}
	return &cp, nil
}

func (r *stubProviderRepo) ListAll() ([]*domain.PaymentProviderConfig, error) {
	if r.provider == nil {
		return nil, nil
	}
	return []*domain.PaymentProviderConfig{r.provider}, nil
}

func (r *stubProviderRepo) ListEnabled() ([]*domain.PaymentProviderConfig, error) {
	if r.provider == nil || !r.provider.Enabled {
		return nil, nil
	}
	return []*domain.PaymentProviderConfig{r.provider}, nil
}

func (r *stubProviderRepo) Update(p *domain.PaymentProviderConfig) error {
	r.provider = p
	return nil
}

func (r *stubProviderRepo) Delete(id string) error {
	r.provider = nil
	return nil
}

type noOpIDGen struct{}

func (noOpIDGen) NewID() string { return "id-1" }

type memoryAttemptRepo struct {
	attempts []*domain.PaymentAttempt
}

func (r *memoryAttemptRepo) Create(_ context.Context, attempt *domain.PaymentAttempt) error {
	cp := *attempt
	r.attempts = append(r.attempts, &cp)
	return nil
}

func (r *memoryAttemptRepo) Update(_ context.Context, attempt *domain.PaymentAttempt) error {
	for i := range r.attempts {
		if r.attempts[i].ID == attempt.ID {
			cp := *attempt
			r.attempts[i] = &cp
			return nil
		}
	}
	cp := *attempt
	r.attempts = append(r.attempts, &cp)
	return nil
}

func (r *memoryAttemptRepo) FindLatestByOrderID(_ context.Context, orderID string) (*domain.PaymentAttempt, error) {
	for i := len(r.attempts) - 1; i >= 0; i-- {
		if r.attempts[i].OrderID == orderID {
			cp := *r.attempts[i]
			return &cp, nil
		}
	}
	return nil, domain.ErrPaymentAttemptNotFound
}

func (r *memoryAttemptRepo) FindByOutTradeNo(_ context.Context, outTradeNo string) (*domain.PaymentAttempt, error) {
	for i := len(r.attempts) - 1; i >= 0; i-- {
		if r.attempts[i].OutTradeNo == outTradeNo {
			cp := *r.attempts[i]
			return &cp, nil
		}
	}
	return nil, domain.ErrPaymentAttemptNotFound
}

func (r *memoryAttemptRepo) ListPending(_ context.Context, limit int) ([]*domain.PaymentAttempt, error) {
	if limit <= 0 {
		limit = len(r.attempts)
	}
	out := make([]*domain.PaymentAttempt, 0, len(r.attempts))
	for _, attempt := range r.attempts {
		if attempt.Status == domain.ChargeStatusPending {
			cp := *attempt
			out = append(out, &cp)
			if len(out) == limit {
				break
			}
		}
	}
	return out, nil
}

type epayTestProvider struct {
	merchantKey string
	createOrder string
	createCalls *int
	queryResult *domain.PaymentOrderQueryResult
}

func (p *epayTestProvider) CreateCharge(_ context.Context, orderID string, currency string, amountMinor int64) (*domain.ChargeResult, error) {
	if p.createCalls != nil {
		*p.createCalls = *p.createCalls + 1
	}
	p.createOrder = orderID
	return &domain.ChargeResult{
		ChargeID:   "epay-local-1",
		OutTradeNo: orderID,
		Status:     domain.ChargeStatusPending,
		PaymentURL: "https://pay.example.com/submit.php?out_trade_no=" + orderID,
	}, nil
}

func (p *epayTestProvider) VerifyWebhook(rawBody []byte, headers domain.WebhookHeaders) (*domain.WebhookPayload, error) {
	values, err := url.ParseQuery(string(rawBody))
	if err != nil {
		return nil, err
	}
	actual := strings.TrimSpace(values.Get("sign"))
	if actual == "" {
		return nil, errors.New("missing sign")
	}
	if expected := epayTestSign(values, p.merchantKey); expected != strings.ToLower(actual) {
		return nil, errors.New("signature mismatch")
	}
	status := domain.ChargeStatusPending
	switch strings.ToUpper(strings.TrimSpace(values.Get("trade_status"))) {
	case "TRADE_SUCCESS", "SUCCESS", "PAID":
		status = domain.ChargeStatusSuccess
	case "TRADE_CLOSED", "FAILED":
		status = domain.ChargeStatusFailed
	}
	chargeID := strings.TrimSpace(values.Get("trade_no"))
	if chargeID == "" {
		chargeID = strings.TrimSpace(values.Get("out_trade_no"))
	}
	return &domain.WebhookPayload{
		ChargeID:  chargeID,
		OrderID:   strings.TrimSpace(values.Get("out_trade_no")),
		Status:    status,
		RawBody:   rawBody,
		Signature: actual,
	}, nil
}

func (p *epayTestProvider) QueryOrder(_ context.Context, query domain.PaymentOrderQuery) (*domain.PaymentOrderQueryResult, error) {
	if p.queryResult != nil {
		return p.queryResult, nil
	}
	orderID := strings.TrimSpace(query.OutTradeNo)
	if orderID == "" {
		orderID = "order-1"
	}
	return &domain.PaymentOrderQueryResult{
		ChargeID:           "epay-queried-1",
		OrderID:            orderID,
		Status:             domain.ChargeStatusSuccess,
		Amount:             "0.07",
		ProviderMerchantID: "merch_1",
		RawBody:            []byte(`{"code":1}`),
	}, nil
}

func TestHandleProviderReturn_EPaySuccessConfirmsOrder(t *testing.T) {
	order := app.PayableOrder{
		ID:          "order-1",
		Status:      "pending",
		CustomerID:  "cust-1",
		ProductID:   "prod-1",
		Currency:    "USD",
		PriceAmount: 7,
	}
	orders := &mockOrderActivator{order: order}
	orch := app.NewPostPaymentOrchestrator(
		orders,
		&mockProductPurchaser{},
		nil,
		&mockInvoiceCreator{},
		nil,
	)
	providerSvc := newEPayProviderServiceForTest("provider-1", "merch_1", "secret")
	svc := app.NewPaymentAppService(providerSvc, orch, nil)

	result, err := svc.HandleProviderReturn("provider-1", epayReturnQuery(url.Values{
		"pid":          {"merch_1"},
		"out_trade_no": {"order-1"},
		"trade_no":     {"epay-1"},
		"trade_status": {"TRADE_SUCCESS"},
		"money":        {"0.07"},
	}, "secret"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.OrderID != "order-1" {
		t.Fatalf("expected order-1, got %s", result.OrderID)
	}
	if result.Status != domain.ChargeStatusSuccess {
		t.Fatalf("expected success, got %s", result.Status)
	}
}

func TestHandleProviderReturn_RejectsAmountMismatch(t *testing.T) {
	order := app.PayableOrder{
		ID:          "order-1",
		Status:      "pending",
		CustomerID:  "cust-1",
		ProductID:   "prod-1",
		InvoiceID:   "inv-1",
		Currency:    "USD",
		PriceAmount: 7,
	}
	orch := app.NewPostPaymentOrchestrator(
		&mockOrderActivator{order: order},
		&mockProductPurchaser{},
		nil,
		&mockInvoiceCreator{},
		nil,
	)
	providerSvc := newEPayProviderServiceForTest("provider-1", "merch_1", "secret")
	svc := app.NewPaymentAppService(providerSvc, orch, nil)

	_, err := svc.HandleProviderReturn("provider-1", epayReturnQuery(url.Values{
		"pid":          {"merch_1"},
		"out_trade_no": {"order-1"},
		"trade_no":     {"epay-1"},
		"trade_status": {"TRADE_SUCCESS"},
		"money":        {"0.08"},
	}, "secret"))
	if err == nil {
		t.Fatal("expected amount mismatch error")
	}
	var appErr *apperr.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected *AppError, got %T", err)
	}
	if appErr.Code != apperr.CodeWebhookFailed {
		t.Fatalf("expected code %s, got %s", apperr.CodeWebhookFailed, appErr.Code)
	}
}

func TestReconcileEPayOrder_QuerySuccessConfirmsOrder(t *testing.T) {
	order := app.PayableOrder{
		ID:          "order-1",
		Status:      "pending",
		CustomerID:  "cust-1",
		ProductID:   "prod-1",
		Currency:    "USD",
		PriceAmount: 7,
	}
	orders := &mockOrderActivator{order: order}
	orch := app.NewPostPaymentOrchestrator(
		orders,
		&mockProductPurchaser{},
		nil,
		&mockInvoiceCreator{},
		nil,
	)
	providerSvc := newEPayProviderServiceForTest("provider-1", "merch_1", "secret")
	svc := app.NewPaymentAppService(providerSvc, orch, nil)

	result, err := svc.ReconcileEPayOrder(context.Background(), "provider-1", "order-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != domain.ChargeStatusSuccess {
		t.Fatalf("expected success, got %s", result.Status)
	}
	if !result.Updated {
		t.Fatal("expected query reconciliation to update order")
	}
	if !orders.activateHit {
		t.Fatal("expected order activation")
	}
}

func TestInitiatePayment_RecordsEPayAttempt(t *testing.T) {
	order := app.PayableOrder{
		ID:          "order-1",
		Status:      "pending",
		CustomerID:  "cust-1",
		ProductID:   "prod-1",
		Currency:    "USD",
		PriceAmount: 7,
	}
	orch := app.NewPostPaymentOrchestrator(
		&mockOrderActivator{order: order},
		&mockProductPurchaser{},
		nil,
		&mockInvoiceCreator{},
		nil,
	)
	providerSvc := newEPayProviderServiceForTest("provider-1", "merch_1", "secret")
	attempts := &memoryAttemptRepo{}
	svc := app.NewPaymentAppService(providerSvc, orch, nil)
	svc.SetPaymentAttemptStore(attempts, noOpIDGen{})

	result, err := svc.InitiatePayment(context.Background(), &app.InitiatePaymentRequest{
		OrderID:    "order-1",
		ProviderID: "provider-1",
		PayType:    "alipay",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.PaymentURL == "" {
		t.Fatal("expected payment url")
	}
	if len(attempts.attempts) != 1 {
		t.Fatalf("expected one payment attempt, got %d", len(attempts.attempts))
	}
	attempt := attempts.attempts[0]
	if attempt.OrderID != "order-1" || attempt.ProviderID != "provider-1" || attempt.PayType != "alipay" {
		t.Fatalf("unexpected attempt: %+v", attempt)
	}
	if attempt.OutTradeNo == "" || attempt.OutTradeNo == "order-1" {
		t.Fatalf("expected independent out_trade_no, got %+v", attempt)
	}
	if attempt.PayURL != result.PaymentURL || attempt.Status != domain.ChargeStatusPending {
		t.Fatalf("unexpected attempt fields: %+v", attempt)
	}
	if !strings.Contains(result.PaymentURL, "out_trade_no="+url.QueryEscape(attempt.OutTradeNo)) {
		t.Fatalf("expected payment url to contain attempt out_trade_no, got %s", result.PaymentURL)
	}
	if attempt.TradeNo != "" {
		t.Fatalf("expected empty trade_no before callback, got %s", attempt.TradeNo)
	}
}

func TestReconcileEPayOrder_UsesPersistedAttemptProvider(t *testing.T) {
	order := app.PayableOrder{
		ID:          "order-1",
		Status:      "pending",
		CustomerID:  "cust-1",
		ProductID:   "prod-1",
		Currency:    "USD",
		PriceAmount: 7,
	}
	orders := &mockOrderActivator{order: order}
	orch := app.NewPostPaymentOrchestrator(
		orders,
		&mockProductPurchaser{},
		nil,
		&mockInvoiceCreator{},
		nil,
	)
	providerSvc := newEPayProviderServiceForTest("provider-1", "merch_1", "secret")
	attempts := &memoryAttemptRepo{}
	if err := attempts.Create(context.Background(), &domain.PaymentAttempt{
		ID:         "attempt-1",
		OrderID:    "order-1",
		ProviderID: "provider-1",
		PayType:    "alipay",
		OutTradeNo: "order-1",
		Status:     domain.ChargeStatusPending,
	}); err != nil {
		t.Fatalf("create attempt: %v", err)
	}
	svc := app.NewPaymentAppService(providerSvc, orch, nil)
	svc.SetPaymentAttemptStore(attempts, noOpIDGen{})

	result, err := svc.ReconcileEPayOrder(context.Background(), "", "order-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ProviderID != "provider-1" {
		t.Fatalf("expected provider-1, got %s", result.ProviderID)
	}
	if !result.Updated || !orders.activateHit {
		t.Fatal("expected order activation through attempt provider")
	}
	updated, err := attempts.FindByOutTradeNo(context.Background(), "order-1")
	if err != nil {
		t.Fatalf("find attempt: %v", err)
	}
	if updated.Status != domain.ChargeStatusSuccess || updated.TradeNo != "epay-queried-1" {
		t.Fatalf("expected attempt success update, got %+v", updated)
	}
}

func TestInitiatePayment_ReusesPendingEPayAttemptForOrder(t *testing.T) {
	order := app.PayableOrder{
		ID:          "order-1",
		Status:      "pending",
		CustomerID:  "cust-1",
		ProductID:   "prod-1",
		Currency:    "USD",
		PriceAmount: 7,
	}
	invoices := &mockInvoiceCreator{}
	orch := app.NewPostPaymentOrchestrator(
		&mockOrderActivator{order: order},
		&mockProductPurchaser{},
		nil,
		invoices,
		nil,
	)
	createCalls := 0
	providerSvc := newEPayProviderServiceForTestWithCreateCounter("provider-1", "merch_1", "secret", &createCalls)
	attempts := &memoryAttemptRepo{}
	svc := app.NewPaymentAppService(providerSvc, orch, nil)
	svc.SetPaymentAttemptStore(attempts, noOpIDGen{})

	first, err := svc.InitiatePayment(context.Background(), &app.InitiatePaymentRequest{
		OrderID:    "order-1",
		ProviderID: "provider-1",
		PayType:    "alipay",
	})
	if err != nil {
		t.Fatalf("first payment unexpected error: %v", err)
	}
	second, err := svc.InitiatePayment(context.Background(), &app.InitiatePaymentRequest{
		OrderID:    "order-1",
		ProviderID: "provider-1",
		PayType:    "alipay",
	})
	if err != nil {
		t.Fatalf("second payment unexpected error: %v", err)
	}

	if createCalls != 1 {
		t.Fatalf("expected one provider charge creation, got %d", createCalls)
	}
	if invoices.createCount != 1 {
		t.Fatalf("expected one invoice creation, got %d", invoices.createCount)
	}
	if len(attempts.attempts) != 1 {
		t.Fatalf("expected one payment attempt, got %d", len(attempts.attempts))
	}
	if second.PaymentURL != first.PaymentURL {
		t.Fatalf("expected reused payment url %s, got %s", first.PaymentURL, second.PaymentURL)
	}
	if second.InvoiceID != first.InvoiceID || second.Status != domain.ChargeStatusPending {
		t.Fatalf("unexpected reused response: first=%+v second=%+v", first, second)
	}
	if second.Message != "existing pending payment returned" {
		t.Fatalf("expected idempotent reuse message, got %q", second.Message)
	}
}

func newEPayProviderServiceForTest(providerID, pid, merchantKey string) *app.ProviderAppService {
	return newEPayProviderServiceForTestWithCreateCounter(providerID, pid, merchantKey, nil)
}

func newEPayProviderServiceForTestWithCreateCounter(providerID, pid, merchantKey string, createCalls *int) *app.ProviderAppService {
	repo := &stubProviderRepo{
		provider: &domain.PaymentProviderConfig{
			ID:        providerID,
			Type:      domain.ProviderTypeEPay,
			Name:      "EPay",
			Enabled:   true,
			SortOrder: 0,
			Config: map[string]interface{}{
				"pid":          pid,
				"merchant_key": merchantKey,
				"api_url":      "https://pay.example.com",
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
	}
	providerSvc := app.NewProviderAppService(repo, noOpIDGen{})
	providerSvc.RegisterNotifyURLBuilder(func(providerID string) (string, error) {
		return "https://example.com/api/v1/payments/webhook/epay/" + providerID, nil
	})
	providerSvc.RegisterPublicBaseURLBuilder(func() (string, error) {
		return "https://example.com", nil
	})
	providerSvc.RegisterFactory(domain.ProviderTypeEPay, func(cfg *domain.PaymentProviderConfig, cb func(*domain.WebhookPayload)) domain.PaymentProvider {
		key, _ := cfg.Config["merchant_key"].(string)
		return &epayTestProvider{merchantKey: key, createCalls: createCalls}
	})
	return providerSvc
}

func epayReturnQuery(values url.Values, key string) []byte {
	values.Set("sign", epayTestSign(values, key))
	values.Set("sign_type", "MD5")
	return []byte(values.Encode())
}

func epayTestSign(values url.Values, key string) string {
	keys := make([]string, 0, len(values))
	for k := range values {
		if strings.EqualFold(k, "sign") || strings.EqualFold(k, "sign_type") {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		if values.Get(k) == "" {
			continue
		}
		pairs = append(pairs, fmt.Sprintf("%s=%s", k, values.Get(k)))
	}
	hash := md5.Sum([]byte(strings.Join(pairs, "&") + key))
	return strings.ToLower(hex.EncodeToString(hash[:]))
}

func TestHandleProviderReturn_MapsAttemptOutTradeNoToOrder(t *testing.T) {
	order := app.PayableOrder{
		ID:          "order-1",
		Status:      "pending",
		CustomerID:  "cust-1",
		ProductID:   "prod-1",
		Currency:    "USD",
		PriceAmount: 7,
	}
	orders := &mockOrderActivator{order: order}
	orch := app.NewPostPaymentOrchestrator(
		orders,
		&mockProductPurchaser{},
		nil,
		&mockInvoiceCreator{},
		nil,
	)
	providerSvc := newEPayProviderServiceForTest("provider-1", "merch_1", "secret")
	attempts := &memoryAttemptRepo{}
	if err := attempts.Create(context.Background(), &domain.PaymentAttempt{
		ID:         "attempt-1",
		OrderID:    "order-1",
		ProviderID: "provider-1",
		PayType:    "alipay",
		OutTradeNo: "pay-attempt-1",
		Status:     domain.ChargeStatusPending,
	}); err != nil {
		t.Fatalf("create attempt: %v", err)
	}
	svc := app.NewPaymentAppService(providerSvc, orch, nil)
	svc.SetPaymentAttemptStore(attempts, noOpIDGen{})

	result, err := svc.HandleProviderReturn("provider-1", epayReturnQuery(url.Values{
		"pid":          {"merch_1"},
		"out_trade_no": {"pay-attempt-1"},
		"trade_no":     {"epay-1"},
		"trade_status": {"TRADE_SUCCESS"},
		"money":        {"0.07"},
	}, "secret"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.OrderID != "order-1" || !orders.activateHit {
		t.Fatalf("expected mapped order activation, result=%+v activate=%v", result, orders.activateHit)
	}
	updated, err := attempts.FindByOutTradeNo(context.Background(), "pay-attempt-1")
	if err != nil {
		t.Fatalf("find attempt: %v", err)
	}
	if updated.TradeNo != "epay-1" || updated.Status != domain.ChargeStatusSuccess {
		t.Fatalf("expected attempt update, got %+v", updated)
	}
}

type mockCryptoProvider struct {
	chargeResult *domain.ChargeResult
	chargeErr    error
}

func (m *mockCryptoProvider) CreateCharge(_ context.Context, orderID, currency string, amountMinor int64) (*domain.ChargeResult, error) {
	return m.chargeResult, m.chargeErr
}
func (m *mockCryptoProvider) VerifyWebhook(rawBody []byte, headers domain.WebhookHeaders) (*domain.WebhookPayload, error) {
	return nil, nil
}
func (m *mockCryptoProvider) CreateCryptoCharge(_ context.Context, orderID string, amountMinor int64, network domain.CryptoNetwork) (*domain.ChargeResult, error) {
	return m.chargeResult, m.chargeErr
}
func (m *mockCryptoProvider) GetNetworks() []domain.NetworkInfo {
	return domain.DefaultNetworkInfos()
}
func (m *mockCryptoProvider) GetChargeDetail(chargeID string) *domain.CryptoChargeDetail {
	return nil
}

// ── Tests ──────────────────────────────────────────────────────────────

func TestInitiatePayment_ValidationErrors(t *testing.T) {
	orch := app.NewPostPaymentOrchestrator(
		&mockOrderActivator{order: app.PayableOrder{Status: "pending"}},
		&mockProductPurchaser{},
		&mockInstanceCreator{},
		&mockInvoiceCreator{},
		nil,
	)
	svc := app.NewPaymentAppService(nil, orch, nil)

	// Empty order ID
	_, err := svc.InitiatePayment(context.Background(), &app.InitiatePaymentRequest{})
	if err == nil {
		t.Fatal("expected error for empty order_id")
	}
	var appErr *apperr.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected *AppError, got %T", err)
	}
	if appErr.Code != apperr.CodeInvalidParams {
		t.Fatalf("expected code %s, got %s", apperr.CodeInvalidParams, appErr.Code)
	}
}

func TestHandlePaymentConfirmed_UsesOrderNetworkModeSnapshotForPendingInstanceAndProvisioning(t *testing.T) {
	instances := &mockInstanceCreator{}
	products := &mockProductPurchaser{}
	orch := app.NewPostPaymentOrchestrator(
		&mockOrderActivator{order: app.PayableOrder{
			ID:          "order-1",
			CustomerID:  "cust-1",
			ProductID:   "prod-1",
			Hostname:    "web-01",
			Plan:        "free",
			Region:      "region-1",
			OS:          "ubuntu-22.04",
			NetworkMode: "nat",
			CPU:         1,
			MemoryMB:    1024,
			DiskGB:      25,
			Currency:    "USD",
			PriceAmount: 100,
		}},
		products,
		instances,
		nil,
		nil,
	)

	if err := orch.HandlePaymentConfirmed("order-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if instances.networkMode != "nat" {
		t.Fatalf("expected pending instance network mode nat, got %s", instances.networkMode)
	}
	if products.networkMode != "nat" {
		t.Fatalf("expected provisioning event network mode nat, got %s", products.networkMode)
	}
}

func TestInitiatePayment_OrderNotFound(t *testing.T) {
	orch := app.NewPostPaymentOrchestrator(
		&mockOrderActivator{getErr: errors.New("not found")},
		&mockProductPurchaser{},
		nil, nil, nil,
	)
	svc := app.NewPaymentAppService(nil, orch, nil)

	_, err := svc.InitiatePayment(context.Background(), &app.InitiatePaymentRequest{OrderID: "order-999"})
	if err == nil {
		t.Fatal("expected error for missing order")
	}
	var appErr *apperr.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected *AppError, got %T", err)
	}
	if appErr.Code != apperr.CodeOrderNotFound {
		t.Fatalf("expected code %s, got %s", apperr.CodeOrderNotFound, appErr.Code)
	}
}

func TestInitiatePayment_OrderNotPending(t *testing.T) {
	orch := app.NewPostPaymentOrchestrator(
		&mockOrderActivator{order: app.PayableOrder{Status: "active"}},
		&mockProductPurchaser{},
		nil, nil, nil,
	)
	svc := app.NewPaymentAppService(nil, orch, nil)

	_, err := svc.InitiatePayment(context.Background(), &app.InitiatePaymentRequest{OrderID: "order-1"})
	if err == nil {
		t.Fatal("expected error for non-pending order")
	}
	var appErr *apperr.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected *AppError, got %T", err)
	}
	if appErr.Code != apperr.CodeOrderNotPending {
		t.Fatalf("expected code %s, got %s", apperr.CodeOrderNotPending, appErr.Code)
	}
}

func TestInitiatePayment_CryptoSuccess(t *testing.T) {
	orch := app.NewPostPaymentOrchestrator(
		&mockOrderActivator{order: app.PayableOrder{
			ID: "order-1", Status: "pending", PriceAmount: 2999,
		}},
		&mockProductPurchaser{},
		nil,
		&mockInvoiceCreator{},
		nil,
	)
	crypto := &mockCryptoProvider{
		chargeResult: &domain.ChargeResult{
			ChargeID: "crypto_arb_1",
			Status:   domain.ChargeStatusPending,
		},
	}
	svc := app.NewPaymentAppService(nil, orch, crypto)

	resp, err := svc.InitiatePayment(context.Background(), &app.InitiatePaymentRequest{
		OrderID: "order-1",
		Network: "arbitrum",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ChargeID != "crypto_arb_1" {
		t.Fatalf("expected charge ID crypto_arb_1, got %s", resp.ChargeID)
	}
	if resp.Status != domain.ChargeStatusPending {
		t.Fatalf("expected status pending, got %s", resp.Status)
	}
}

func TestInitiatePayment_UnsupportedNetwork(t *testing.T) {
	orch := app.NewPostPaymentOrchestrator(
		&mockOrderActivator{order: app.PayableOrder{
			ID: "order-1", Status: "pending", PriceAmount: 1000,
		}},
		&mockProductPurchaser{},
		nil, &mockInvoiceCreator{}, nil,
	)
	crypto := &mockCryptoProvider{}
	svc := app.NewPaymentAppService(nil, orch, crypto)

	_, err := svc.InitiatePayment(context.Background(), &app.InitiatePaymentRequest{
		OrderID: "order-1",
		Network: "invalid_network",
	})
	if err == nil {
		t.Fatal("expected error for unsupported network")
	}
	var appErr *apperr.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected *AppError, got %T", err)
	}
	if appErr.Code != apperr.CodeNetworkUnsupported {
		t.Fatalf("expected code %s, got %s", apperr.CodeNetworkUnsupported, appErr.Code)
	}
}
