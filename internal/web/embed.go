//go:build !frontend

package web

import "net/http"

// StaticFs returns nil when built without the "frontend" build tag.
// The caller should check for nil before registering static file routes.
func StaticFs() http.FileSystem { return nil }

// SPAHandler returns nil when built without the "frontend" build tag.
func SPAHandler() http.Handler { return nil }
