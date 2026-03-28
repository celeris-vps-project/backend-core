package infra

import (
	"backend-core/internal/payment/domain"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// CryptoPaymentConfig holds the configuration for the crypto payment provider.
type CryptoPaymentConfig struct {
	// Wallets maps each network to the receiving wallet address.
	Wallets map[domain.CryptoNetwork]string

	// PaymentTimeout is the duration before a charge expires (default: 30m).
	PaymentTimeout time.Duration

	// MockMode: when true, charges are auto-confirmed after a short delay
	// (simulating blockchain confirmation). When false, the provider waits
	// for a real webhook from a blockchain monitor service.
	MockMode bool

	// MockConfirmDelay is the delay before auto-confirming in mock mode (default: 3s).
	MockConfirmDelay time.Duration
}

// DefaultCryptoConfig returns sensible defaults for development.
func DefaultCryptoConfig() CryptoPaymentConfig {
	return CryptoPaymentConfig{
		Wallets: map[domain.CryptoNetwork]string{
			domain.NetworkArbitrum: "0x742d35Cc6634C0532925a3b844Bc9e7595f2bD3E",
			domain.NetworkSolana:   "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v",
			domain.NetworkTRC20:    "TN3W4H6rK2ce4vX9YnFQHwKENnHjoxb3m9",
			domain.NetworkBSC:      "0x742d35Cc6634C0532925a3b844Bc9e7595f2bD3E",
			domain.NetworkPolygon:  "0x742d35Cc6634C0532925a3b844Bc9e7595f2bD3E",
		},
		PaymentTimeout:   30 * time.Minute,
		MockMode:         true,
		MockConfirmDelay: 3 * time.Second,
	}
}

// CryptoPaymentProvider implements domain.CryptoPaymentProvider for USDT payments
// across multiple blockchain networks.
//
// In MockMode it auto-confirms payments after a short delay (simulating
// blockchain confirmations). In production mode it waits for external
// webhook callbacks from a blockchain monitoring service.
type CryptoPaymentProvider struct {
	config   *CryptoPaymentConfig
	networks []domain.NetworkInfo
	charges  sync.Map // chargeID → *chargeRecord
	seq      atomic.Int64
	callback func(payload *domain.WebhookPayload)
}

// chargeRecord is the internal representation of a pending crypto charge.
type chargeRecord struct {
	ChargeID      string
	OrderID       string
	Network       domain.CryptoNetwork
	WalletAddress string
	AmountUSDT    string
	ExpiresAt     time.Time
	Status        string // pending, success, failed, expired
}

// NewCryptoPaymentProvider creates a new USDT payment provider.
func NewCryptoPaymentProvider(cfg *CryptoPaymentConfig, onWebhook func(payload *domain.WebhookPayload)) *CryptoPaymentProvider {
	return &CryptoPaymentProvider{
		config:   cfg,
		networks: domain.DefaultNetworkInfos(),
		callback: onWebhook,
	}
}

// SetCallback updates the webhook callback function (breaks circular dependency).
func (p *CryptoPaymentProvider) SetCallback(cb func(payload *domain.WebhookPayload)) {
	p.callback = cb
}

// ── PaymentProvider interface ──────────────────────────────────────────

// CreateCharge implements domain.PaymentProvider.
// Uses Arbitrum as the default network when called through the generic interface.
func (p *CryptoPaymentProvider) CreateCharge(ctx context.Context, orderID string, currency string, amountMinor int64) (*domain.ChargeResult, error) {
	return p.CreateCryptoCharge(ctx, orderID, amountMinor, domain.NetworkArbitrum)
}

// VerifyWebhook implements domain.PaymentProvider.
// Validates the webhook payload from the blockchain monitor service.
func (p *CryptoPaymentProvider) VerifyWebhook(rawBody []byte, signature string) (*domain.WebhookPayload, error) {
	// In production: verify HMAC signature from the blockchain monitor.
	// For now: parse the JSON body and trust it (same as mock provider).
	var body struct {
		ChargeID string `json:"charge_id"`
		OrderID  string `json:"order_id"`
		Status   string `json:"status"`
		TxHash   string `json:"tx_hash"`
	}
	if err := json.Unmarshal(rawBody, &body); err != nil {
		return nil, fmt.Errorf("invalid webhook body: %w", err)
	}

	// Update the internal charge record
	if rec, ok := p.charges.Load(body.ChargeID); ok {
		r := rec.(*chargeRecord)
		r.Status = body.Status
	}

	return &domain.WebhookPayload{
		ChargeID:  body.ChargeID,
		OrderID:   body.OrderID,
		Status:    body.Status,
		RawBody:   rawBody,
		Signature: signature,
	}, nil
}

// ── CryptoPaymentProvider interface ────────────────────────────────────

// CreateCryptoCharge creates a USDT payment charge on a specific blockchain network.
func (p *CryptoPaymentProvider) CreateCryptoCharge(_ context.Context, orderID string, amountMinor int64, network domain.CryptoNetwork) (*domain.ChargeResult, error) {
	if !domain.ValidNetwork(string(network)) {
		return nil, fmt.Errorf("unsupported network: %s", network)
	}

	wallet, ok := p.config.Wallets[network]
	if !ok || wallet == "" {
		return nil, fmt.Errorf("no wallet configured for network: %s", network)
	}

	chargeID := fmt.Sprintf("crypto_%s_%d", network, p.seq.Add(1))

	// Convert minor units (cents) to USDT decimal string.
	// USDT has 6 decimals on most chains, but our internal representation
	// uses cents (2 decimals), so $29.99 = 2999 cents = "29.99" USDT.
	amountUSDT := fmt.Sprintf("%.2f", float64(amountMinor)/100.0)

	expiresAt := time.Now().Add(p.config.PaymentTimeout)
	qrData := domain.BuildQRData(network, wallet, amountUSDT)

	// Find display name for this network
	networkName := string(network)
	for _, ni := range p.networks {
		if ni.Network == network {
			networkName = ni.DisplayName
			break
		}
	}

	// Store charge record
	rec := &chargeRecord{
		ChargeID:      chargeID,
		OrderID:       orderID,
		Network:       network,
		WalletAddress: wallet,
		AmountUSDT:    amountUSDT,
		ExpiresAt:     expiresAt,
		Status:        domain.ChargeStatusPending,
	}
	p.charges.Store(chargeID, rec)

	cryptoDetail := &domain.CryptoChargeDetail{
		WalletAddress: wallet,
		Network:       network,
		NetworkName:   networkName,
		AmountUSDT:    amountUSDT,
		QRData:        qrData,
		ExpiresAt:     expiresAt,
	}

	result := &domain.ChargeResult{
		ChargeID:   chargeID,
		Status:     domain.ChargeStatusPending,
		PaymentURL: fmt.Sprintf("/orders/%s/pay", orderID),
		Crypto:     cryptoDetail,
	}

	log.Printf("[CryptoPaymentProvider] charge created: id=%s order=%s network=%s amount=%s USDT wallet=%s",
		chargeID, orderID, network, amountUSDT, wallet)

	// In mock mode: auto-confirm after a delay (simulating blockchain confirmation)
	if p.config.MockMode && p.callback != nil {
		go func() {
			time.Sleep(p.config.MockConfirmDelay)
			log.Printf("[CryptoPaymentProvider] MOCK: auto-confirming charge=%s order=%s", chargeID, orderID)

			rec.Status = domain.ChargeStatusSuccess

			p.callback(&domain.WebhookPayload{
				ChargeID: chargeID,
				OrderID:  orderID,
				Status:   domain.ChargeStatusSuccess,
				RawBody: []byte(fmt.Sprintf(
					`{"charge_id":"%s","order_id":"%s","status":"success","network":"%s","tx_hash":"0xmock_%s"}`,
					chargeID, orderID, network, chargeID,
				)),
				Signature: "mock_crypto_signature",
			})
		}()
	}

	// Schedule expiration check
	go func() {
		time.Sleep(p.config.PaymentTimeout)
		if r, ok := p.charges.Load(chargeID); ok {
			record := r.(*chargeRecord)
			if record.Status == domain.ChargeStatusPending {
				record.Status = "expired"
				log.Printf("[CryptoPaymentProvider] charge expired: id=%s order=%s", chargeID, orderID)
			}
		}
	}()

	return result, nil
}

// GetNetworks returns all supported blockchain networks and their info.
func (p *CryptoPaymentProvider) GetNetworks() []domain.NetworkInfo {
	// Return only enabled networks that have a configured wallet
	var result []domain.NetworkInfo
	for _, ni := range p.networks {
		if _, hasWallet := p.config.Wallets[ni.Network]; hasWallet && ni.Enabled {
			result = append(result, ni)
		}
	}
	return result
}

// GetChargeDetail returns the crypto-specific details for a charge.
func (p *CryptoPaymentProvider) GetChargeDetail(chargeID string) *domain.CryptoChargeDetail {
	rec, ok := p.charges.Load(chargeID)
	if !ok {
		return nil
	}
	r := rec.(*chargeRecord)

	networkName := string(r.Network)
	for _, ni := range p.networks {
		if ni.Network == r.Network {
			networkName = ni.DisplayName
			break
		}
	}

	return &domain.CryptoChargeDetail{
		WalletAddress: r.WalletAddress,
		Network:       r.Network,
		NetworkName:   networkName,
		AmountUSDT:    r.AmountUSDT,
		QRData:        domain.BuildQRData(r.Network, r.WalletAddress, r.AmountUSDT),
		ExpiresAt:     r.ExpiresAt,
	}
}
