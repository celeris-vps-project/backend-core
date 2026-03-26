// Package apperr defines application-level error codes shared between backend and frontend.
//
// The backend returns JSON error responses in the form:
//
//	{"code": "ERROR_CODE", "error": "human-readable debug message"}
//
// The frontend uses the "code" field to look up a translated message via i18n
// (errors.ERROR_CODE), falling back to the "error" text for debugging.
package apperr

// ── Auth / Identity ──

const (
	CodeInvalidParams  = "INVALID_PARAMS"   // request body validation failed
	CodeUnauthorized   = "UNAUTHORIZED"     // missing or expired JWT
	CodeForbidden      = "FORBIDDEN"        // insufficient permissions
	CodeUserNotFound   = "USER_NOT_FOUND"   // email not registered
	CodeWrongPassword  = "WRONG_PASSWORD"   // password mismatch
	CodeAccountDisabled = "ACCOUNT_DISABLED" // account banned or inactive
	CodeEmailTaken     = "EMAIL_TAKEN"      // email already registered
)

// ── Resource not found ──

const (
	CodeOrderNotFound   = "ORDER_NOT_FOUND"
	CodeProductNotFound = "PRODUCT_NOT_FOUND"
	CodeInstanceNotFound = "INSTANCE_NOT_FOUND"
	CodeInvoiceNotFound = "INVOICE_NOT_FOUND"
	CodeNodeNotFound    = "NODE_NOT_FOUND"
	CodeChargeNotFound  = "CHARGE_NOT_FOUND"
	CodeRegionNotFound  = "REGION_NOT_FOUND"
	CodePoolNotFound    = "POOL_NOT_FOUND"
	CodeTokenNotFound   = "TOKEN_NOT_FOUND"
	CodeIPNotFound      = "IP_NOT_FOUND"
	CodeTaskNotFound     = "TASK_NOT_FOUND"
	CodeProviderNotFound = "PROVIDER_NOT_FOUND"
)

// ── Business logic ──

const (
	CodeOrderNotPending        = "ORDER_NOT_PENDING"
	CodeNoAvailableSlots       = "NO_AVAILABLE_SLOTS"
	CodeInvalidStateTransition = "INVALID_STATE_TRANSITION"
	CodePaymentFailed          = "PAYMENT_FAILED"
	CodeNetworkUnsupported     = "NETWORK_UNSUPPORTED"
	CodeInvoiceInvalidState    = "INVOICE_INVALID_STATE"
	CodeCurrencyMismatch       = "CURRENCY_MISMATCH"
	CodeSlotConflict           = "SLOT_CONFLICT"
	CodeDuplicateLineItem      = "DUPLICATE_LINE_ITEM"
	CodeTokenExpired           = "TOKEN_EXPIRED"
	CodeTokenAlreadyUsed       = "TOKEN_ALREADY_USED"
	CodeNodeDisabled           = "NODE_DISABLED"
	CodeCryptoNotConfigured    = "CRYPTO_NOT_CONFIGURED"
)

// ── Generic ──

const (
	CodeInternalError  = "INTERNAL_ERROR"
	CodeWebhookFailed  = "WEBHOOK_FAILED"
	CodeAlreadyPaid    = "ALREADY_PAID"
)
