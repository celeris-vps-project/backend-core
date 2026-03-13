// Package infra — this file is intentionally left as a thin redirect.
//
// The InMemoryDelayedPublisher has been moved to pkg/delayed as a generic
// infrastructure component shared across bounded contexts. This file remains
// to preserve git history context.
//
// New code should import "backend-core/pkg/delayed" directly:
//
//	import "backend-core/pkg/delayed"
//	pub := delayed.NewInMemoryPublisher(router.Dispatch)
//
// See also:
//   - pkg/delayed/publisher.go  — Publisher interface + InMemoryPublisher
//   - pkg/delayed/router.go     — topic-based multi-handler Router
package infra
