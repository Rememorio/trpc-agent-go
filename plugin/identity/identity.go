//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package identity propagates trusted user identity through agent tool calls.
package identity

import (
	"context"
)

// Identity represents a resolved user identity with credentials and metadata
// that can be injected into tool calls.
type Identity struct {
	// UserID is the authenticated user identifier.
	UserID string

	// Token is an opaque bearer token (e.g., OAuth access token).
	Token string

	// Signature is a business-level request signature for custom auth schemes.
	Signature string

	// Headers are key-value pairs to inject into HTTP-based tool calls
	// (e.g., MCP SSE/Streamable HTTP, webhook invocations).
	Headers map[string]string

	// EnvVars are key-value pairs to inject as environment variables
	// for command-execution tools (e.g., workspace_exec, skill_run).
	EnvVars map[string]string

	// Extra holds arbitrary extension data that business code may need.
	Extra map[string]any
}

// Provider resolves the current user identity from the execution context.
// Implementations typically extract identity from HTTP request headers, JWT
// claims, session stores, or business-specific signing services.
type Provider interface {
	Resolve(ctx context.Context, userID string, sessionID string) (*Identity, error)
}

// ProviderFunc is a convenience adapter to allow the use of ordinary functions
// as Provider implementations.
type ProviderFunc func(ctx context.Context, userID, sessionID string) (*Identity, error)

// Resolve implements Provider.
func (f ProviderFunc) Resolve(ctx context.Context, userID, sessionID string) (*Identity, error) {
	return f(ctx, userID, sessionID)
}

// ---- context helpers ----

type identityCtxKey struct{}

// NewContext returns a copy of ctx that carries the given Identity.
func NewContext(ctx context.Context, id *Identity) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, identityCtxKey{}, id)
}

// FromContext extracts the Identity stored in ctx by NewContext.
// Returns nil and false if no identity is present.
func FromContext(ctx context.Context) (*Identity, bool) {
	if ctx == nil {
		return nil, false
	}
	id, ok := ctx.Value(identityCtxKey{}).(*Identity)
	return id, ok
}

// HeadersFromContext returns a copy of identity headers stored in ctx.
//
// It intentionally matches tool/mcp.DynamicHeaderFunc, so callers can pass it
// directly to mcp.WithDynamicHeaders without adding an import dependency from
// this package to tool/mcp.
func HeadersFromContext(ctx context.Context) (map[string]string, error) {
	id, ok := FromContext(ctx)
	if !ok || id == nil || len(id.Headers) == 0 {
		return nil, nil
	}
	return cloneStringMap(id.Headers), nil
}

// EnvVarsFromContext returns a copy of identity environment variables stored
// in ctx. It returns nil when no identity env is available.
func EnvVarsFromContext(ctx context.Context) map[string]string {
	id, ok := FromContext(ctx)
	if !ok || id == nil || len(id.EnvVars) == 0 {
		return nil
	}
	return cloneStringMap(id.EnvVars)
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
