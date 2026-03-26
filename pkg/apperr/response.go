package apperr

import "github.com/cloudwego/hertz/pkg/common/utils"

// Resp builds a standard error JSON response with both a machine-readable
// code and a human-readable message.
//
//	{"code": "ERROR_CODE", "error": "descriptive message for debugging"}
//
// The frontend matches on "code" to display the correct i18n translation,
// and falls back to "error" if no translation is found.
func Resp(code, message string) utils.H {
	return utils.H{
		"code":  code,
		"error": message,
	}
}

// RespMap is the same as Resp but returns a map[string]string,
// useful for handlers that don't import hertz utils.H (e.g. checkout).
func RespMap(code, message string) map[string]string {
	return map[string]string{
		"code":  code,
		"error": message,
	}
}
