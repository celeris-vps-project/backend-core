package domain

import (
	"errors"
	"time"
)

var ErrPublicBaseURLRequired = errors.New("public_base_url is required before configuring EPay; set it in admin general settings")

// PaymentProviderConfig represents a payment provider configured by the admin.
// Instead of hardcoding payment methods, admins dynamically add providers
// (e.g. crypto wallets, Stripe, PayPal) and users choose at checkout time.
type PaymentProviderConfig struct {
	ID         string                 `json:"id"`
	Type       string                 `json:"type"` // "crypto_usdt", "stripe", "paypal", "alipay", etc.
	Name       string                 `json:"name"` // Display name, e.g. "USDT (Multi-Chain)"
	Enabled    bool                   `json:"enabled"`
	SortOrder  int                    `json:"sort_order"` // Lower = higher priority in display
	Config     map[string]interface{} `json:"config"`     // Provider-specific configuration (JSON)
	WebhookURL string                 `json:"webhook_url,omitempty"`
	CreatedAt  time.Time              `json:"created_at"`
	UpdatedAt  time.Time              `json:"updated_at"`
}

// Well-known provider types.
const (
	ProviderTypeCryptoUSDT = "crypto_usdt"
	ProviderTypeStripe     = "stripe"
	ProviderTypePayPal     = "paypal"
	ProviderTypeAlipay     = "alipay"
	ProviderTypeWechatPay  = "wechat_pay"
	ProviderTypeEPay       = "epay"
)

// ProviderTypeInfo describes a supported provider type and its expected config fields.
type ProviderTypeInfo struct {
	Type        string              `json:"type"`
	DisplayName string              `json:"display_name"`
	Fields      []ProviderFieldInfo `json:"fields"`
}

// ProviderFieldInfo describes a single configuration field for a provider type.
type ProviderFieldInfo struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Type        string `json:"type"` // "string", "bool", "number", "wallets" (special map type)
	Required    bool   `json:"required"`
	Placeholder string `json:"placeholder"`
	HelpText    string `json:"help_text"`
}

// SupportedProviderTypes returns metadata about all supported provider types
// and their configuration fields. The frontend uses this to render dynamic forms.
func SupportedProviderTypes() []ProviderTypeInfo {
	return []ProviderTypeInfo{
		{
			Type:        ProviderTypeCryptoUSDT,
			DisplayName: "USDT (Crypto Multi-Chain)",
			Fields: []ProviderFieldInfo{
				{Key: "wallets", Label: "Wallet Addresses", Type: "wallets", Required: true, HelpText: "Map of network → receiving wallet address"},
				{Key: "payment_timeout", Label: "Payment Timeout", Type: "string", Required: false, Placeholder: "30m", HelpText: "Duration before charge expires (e.g. 30m, 1h)"},
				{Key: "mock_mode", Label: "Mock Mode", Type: "bool", Required: false, HelpText: "Auto-confirm payments for testing"},
				{Key: "mock_confirm_delay", Label: "Mock Confirm Delay", Type: "string", Required: false, Placeholder: "3s", HelpText: "Delay before auto-confirming in mock mode"},
			},
		},
		{
			Type:        ProviderTypeStripe,
			DisplayName: "Stripe",
			Fields: []ProviderFieldInfo{
				{Key: "api_key", Label: "API Key", Type: "string", Required: true, Placeholder: "sk_...", HelpText: "Stripe secret API key"},
				{Key: "publishable_key", Label: "Publishable Key", Type: "string", Required: true, Placeholder: "pk_...", HelpText: "Stripe publishable key"},
				{Key: "webhook_secret", Label: "Webhook Secret", Type: "string", Required: true, Placeholder: "whsec_...", HelpText: "Stripe webhook signing secret"},
			},
		},
		{
			Type:        ProviderTypePayPal,
			DisplayName: "PayPal",
			Fields: []ProviderFieldInfo{
				{Key: "client_id", Label: "Client ID", Type: "string", Required: true, HelpText: "PayPal REST API client ID"},
				{Key: "client_secret", Label: "Client Secret", Type: "string", Required: true, HelpText: "PayPal REST API client secret"},
				{Key: "sandbox", Label: "Sandbox Mode", Type: "bool", Required: false, HelpText: "Use PayPal sandbox for testing"},
			},
		},
		{
			Type:        ProviderTypeAlipay,
			DisplayName: "Alipay",
			Fields: []ProviderFieldInfo{
				{Key: "app_id", Label: "App ID", Type: "string", Required: true, HelpText: "Alipay application ID"},
				{Key: "private_key", Label: "Private Key", Type: "string", Required: true, HelpText: "RSA private key for signing"},
				{Key: "alipay_public_key", Label: "Alipay Public Key", Type: "string", Required: true, HelpText: "Alipay RSA public key for verification"},
				{Key: "sandbox", Label: "Sandbox Mode", Type: "bool", Required: false, HelpText: "Use Alipay sandbox for testing"},
			},
		},
		{
			Type:        ProviderTypeWechatPay,
			DisplayName: "WeChat Pay",
			Fields: []ProviderFieldInfo{
				{Key: "app_id", Label: "App ID", Type: "string", Required: true, HelpText: "WeChat application ID"},
				{Key: "mch_id", Label: "Merchant ID", Type: "string", Required: true, HelpText: "WeChat Pay merchant ID"},
				{Key: "api_key", Label: "API Key", Type: "string", Required: true, HelpText: "WeChat Pay API v3 key"},
				{Key: "cert_path", Label: "Certificate Path", Type: "string", Required: false, HelpText: "Path to merchant certificate"},
			},
		},
		{
			Type:        ProviderTypeEPay,
			DisplayName: "EPay (易支付)",
			Fields: []ProviderFieldInfo{
				{Key: "api_url", Label: "Gateway URL", Type: "string", Required: true, Placeholder: "https://pay.example.com", HelpText: "EPay gateway base URL (without trailing slash). e.g. https://pay.myzfw.com"},
				{Key: "pid", Label: "Merchant ID (pid)", Type: "string", Required: true, Placeholder: "1001", HelpText: "Your merchant ID (pid) at the EPay gateway"},
				{Key: "merchant_key", Label: "Merchant Key", Type: "string", Required: true, HelpText: "MD5 secret key for EPay v1 signing"},
				{Key: "return_url", Label: "Return URL Override", Type: "string", Required: false, Placeholder: "https://your-site.com/api/v1/payments/return/epay/{provider_id}", HelpText: "Optional. Defaults to the public base URL plus a server-side EPay return verification endpoint."},
				{Key: "product_name", Label: "Product Name Template", Type: "string", Required: false, Placeholder: "VPS Service", HelpText: "Product name sent to EPay. Default: 'VPS Service'"},
				{Key: "mock_mode", Label: "Mock Mode", Type: "bool", Required: false, HelpText: "Auto-confirm payments after a short delay (for testing)"},
				{Key: "mock_confirm_delay", Label: "Mock Confirm Delay", Type: "string", Required: false, Placeholder: "3s", HelpText: "Delay before auto-confirming in mock mode"},
			},
		},
	}
}

// PaymentProviderRepo abstracts persistence of payment provider configurations.
type PaymentProviderRepo interface {
	Create(p *PaymentProviderConfig) error
	GetByID(id string) (*PaymentProviderConfig, error)
	ListAll() ([]*PaymentProviderConfig, error)
	ListEnabled() ([]*PaymentProviderConfig, error)
	Update(p *PaymentProviderConfig) error
	Delete(id string) error
}
