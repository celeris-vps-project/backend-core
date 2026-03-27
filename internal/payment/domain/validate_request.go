package domain

import "fmt"

// PaymentRequest 是支付请求的值对象，封装验证逻辑
type PaymentRequest struct {
	OrderID     string
	Currency    string
	AmountMinor int64
	PaymentType PaymentType
	Network     CryptoNetwork // 仅用于 crypto 支付
}

type PaymentType string

const (
	PaymentTypeCrypto PaymentType = "crypto"
	PaymentTypeAlipay PaymentType = "alipay"
	PaymentTypeWechat PaymentType = "wechat"
	PaymentTypeEPay   PaymentType = "epay"
)

// Validate 执行通用验证
func (r *PaymentRequest) Validate() error {
	if r.OrderID == "" {
		return fmt.Errorf("order_id is required")
	}
	if r.AmountMinor <= 0 {
		return fmt.Errorf("amount must be > 0")
	}
	if !r.PaymentType.IsValid() {
		return fmt.Errorf("unsupported payment type: %s", r.PaymentType)
	}

	// 针对不同支付类型的特定验证
	switch r.PaymentType {
	case PaymentTypeCrypto:
		if !ValidNetwork(string(r.Network)) {
			return fmt.Errorf("unsupported network: %s", r.Network)
		}
	}
	return nil
}

func (t PaymentType) IsValid() bool {
	switch t {
	case PaymentTypeCrypto, PaymentTypeAlipay, PaymentTypeWechat, PaymentTypeEPay:
		return true
	}
	return false
}
