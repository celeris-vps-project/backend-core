package domain

import (
	"fmt"
	"time"
)

// CryptoNetwork represents a blockchain network for USDT payments.
type CryptoNetwork string

const (
	NetworkArbitrum CryptoNetwork = "arbitrum" // Arbitrum One (L2) — low fees, ~15s
	NetworkSolana   CryptoNetwork = "solana"   // Solana — ultra-fast, ~1s
	NetworkTRC20    CryptoNetwork = "trc20"    // TRON TRC-20 — very low fees
	NetworkBSC      CryptoNetwork = "bsc"      // BNB Smart Chain — low fees
	NetworkPolygon  CryptoNetwork = "polygon"  // Polygon PoS — low fees
)

// AllNetworks returns all supported networks in display order.
func AllNetworks() []CryptoNetwork {
	return []CryptoNetwork{
		NetworkArbitrum,
		NetworkSolana,
		NetworkTRC20,
		NetworkBSC,
		NetworkPolygon,
	}
}

// ValidNetwork checks if a network string is a supported network.
func ValidNetwork(s string) bool {
	switch CryptoNetwork(s) {
	case NetworkArbitrum, NetworkSolana, NetworkTRC20, NetworkBSC, NetworkPolygon:
		return true
	}
	return false
}

// NetworkInfo describes a supported blockchain network and its properties.
type NetworkInfo struct {
	Network          CryptoNetwork `json:"network"`
	DisplayName      string        `json:"display_name"`
	NativeToken      string        `json:"native_token"`
	ContractStandard string        `json:"contract_standard"` // ERC-20, SPL, TRC-20, BEP-20
	EstFeeUSD        float64       `json:"est_fee_usd"`       // estimated transfer fee in USD
	ConfirmationTime string        `json:"confirmation_time"` // human-readable, e.g. "~15s"
	Confirmations    int           `json:"confirmations"`     // blocks to wait
	Enabled          bool          `json:"enabled"`
}

// DefaultNetworkInfos returns the default configuration for all supported networks.
func DefaultNetworkInfos() []NetworkInfo {
	return []NetworkInfo{
		{
			Network:          NetworkArbitrum,
			DisplayName:      "Arbitrum One",
			NativeToken:      "ETH",
			ContractStandard: "ERC-20",
			EstFeeUSD:        0.10,
			ConfirmationTime: "~15s",
			Confirmations:    12,
			Enabled:          true,
		},
		{
			Network:          NetworkSolana,
			DisplayName:      "Solana",
			NativeToken:      "SOL",
			ContractStandard: "SPL",
			EstFeeUSD:        0.001,
			ConfirmationTime: "~1s",
			Confirmations:    1,
			Enabled:          true,
		},
		{
			Network:          NetworkTRC20,
			DisplayName:      "TRON (TRC-20)",
			NativeToken:      "TRX",
			ContractStandard: "TRC-20",
			EstFeeUSD:        1.00,
			ConfirmationTime: "~3s",
			Confirmations:    20,
			Enabled:          true,
		},
		{
			Network:          NetworkBSC,
			DisplayName:      "BNB Smart Chain",
			NativeToken:      "BNB",
			ContractStandard: "BEP-20",
			EstFeeUSD:        0.05,
			ConfirmationTime: "~3s",
			Confirmations:    15,
			Enabled:          true,
		},
		{
			Network:          NetworkPolygon,
			DisplayName:      "Polygon PoS",
			NativeToken:      "MATIC",
			ContractStandard: "ERC-20",
			EstFeeUSD:        0.01,
			ConfirmationTime: "~2s",
			Confirmations:    128,
			Enabled:          true,
		},
	}
}

// CryptoChargeDetail holds crypto-specific payment information returned
// alongside the standard ChargeResult when a crypto payment is initiated.
type CryptoChargeDetail struct {
	WalletAddress string        `json:"wallet_address"` // receiving wallet address
	Network       CryptoNetwork `json:"network"`        // selected network
	NetworkName   string        `json:"network_display"` // human-readable network name
	AmountUSDT    string        `json:"amount_usdt"`    // exact USDT amount (string for precision)
	QRData        string        `json:"qr_data"`        // data encoded in QR code
	ExpiresAt     time.Time     `json:"expires_at"`     // payment deadline
}

// BuildQRData generates the QR code payload for a given network and address.
// Uses EIP-681 for EVM chains, Solana Pay format for Solana, and TRON format for TRC-20.
func BuildQRData(network CryptoNetwork, address string, amountUSDT string) string {
	switch network {
	case NetworkArbitrum, NetworkBSC, NetworkPolygon:
		// EIP-681 format: ethereum:<address>@<chainId>?value=<amount>
		// Simplified: just address + amount for wallet compatibility
		return fmt.Sprintf("ethereum:%s?amount=%s&token=USDT", address, amountUSDT)
	case NetworkSolana:
		return fmt.Sprintf("solana:%s?amount=%s&spl-token=USDT", address, amountUSDT)
	case NetworkTRC20:
		return fmt.Sprintf("tron:%s?amount=%s&token=USDT", address, amountUSDT)
	default:
		return address
	}
}
