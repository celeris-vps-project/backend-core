package grpc

import (
	"context"
	"strings"

	"backend-core/internal/provisioning/app"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type contextKey string

const nodeIDContextKey contextKey = "authenticated-node-id"

// NodeIDFromContext extracts the authenticated node ID set by the auth interceptor.
func NodeIDFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(nodeIDContextKey).(string)
	return v, ok
}

// AuthInterceptor returns a gRPC unary interceptor that:
//   - Skips authentication for the Register RPC (agents use a bootstrap token there).
//   - Validates the "node-token" metadata header for all other RPCs.
//   - Injects the authenticated node ID into the context.
func AuthInterceptor(svc *app.ProvisioningAppService) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		// Skip auth for Register ---?it uses the bootstrap token in the request body
		if strings.HasSuffix(info.FullMethod, "/Register") {
			return handler(ctx, req)
		}

		// Extract "node-token" from gRPC metadata
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Errorf(codes.Unauthenticated, "missing metadata")
		}
		tokens := md.Get("node-token")
		if len(tokens) == 0 || tokens[0] == "" {
			return nil, status.Errorf(codes.Unauthenticated, "missing node-token")
		}

		nodeID, err := svc.ValidateNodeToken(tokens[0])
		if err != nil {
			return nil, status.Errorf(codes.Unauthenticated, "invalid node-token")
		}

		// Inject the authenticated node ID into the context
		ctx = context.WithValue(ctx, nodeIDContextKey, nodeID)
		return handler(ctx, req)
	}
}
