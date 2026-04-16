//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package httpdiag provides a real Runner plugin that injects per-request HTTP
// diagnostics into supported provider SDKs.
//
// The plugin currently supports OpenAI and Anthropic models. It installs SDK
// middleware just before each model call so you can inspect the raw HTTP JSON
// request/response payloads without having to wire provider options manually
// when constructing the model.
//
// All diagnostic output is logged at Debug level through the framework's
// [log.Logger]. To see the output, make sure the log level is set to "debug"
// (e.g. [log.SetLevel]("debug")), or inject a dedicated logger with
// [SetLogger].
//
// Usage:
//
//	import (
//	    "trpc.group/trpc-go/trpc-agent-go/plugin/httpdiag"
//	    "trpc.group/trpc-go/trpc-agent-go/runner"
//	)
//
//	run := runner.NewRunner(
//	    "my-app",
//	    agentInstance,
//	    runner.WithPlugins(
//	        httpdiag.New(
//	            httpdiag.WithRequestBody(),
//	            httpdiag.WithResponseBody(),
//	            httpdiag.WithRewrite200Error(),
//	        ),
//	    ),
//	)
package httpdiag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	anthropicopt "github.com/anthropics/anthropic-sdk-go/option"
	openaiopt "github.com/openai/openai-go/option"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/log"
	modelplugin "trpc.group/trpc-go/trpc-agent-go/model"
	anthropicmodel "trpc.group/trpc-go/trpc-agent-go/model/anthropic"
	openaimodel "trpc.group/trpc-go/trpc-agent-go/model/openai"
	pluginbase "trpc.group/trpc-go/trpc-agent-go/plugin"
)

const defaultPluginName = "httpdiag"

// logger is the logger used by httpdiag. It defaults to [log.Default] and can
// be replaced via [SetLogger]. Diagnostic messages
// are logged at Debug level.
var logger log.Logger = log.Default

// SetLogger replaces the logger used by httpdiag. This allows
// redirecting diagnostic output without affecting the global [log.Default].
func SetLogger(l log.Logger) {
	logger = l
}

// Plugin injects provider-specific HTTP diagnostics just before a model call.
type Plugin struct {
	name            string
	requestBody     bool
	responseBody    bool
	rewrite200Error bool
}

var _ pluginbase.Plugin = (*Plugin)(nil)

// Option configures the httpdiag plugin.
type Option func(*Plugin)

// New creates a new httpdiag Runner plugin.
func New(opts ...Option) pluginbase.Plugin {
	p := &Plugin{name: defaultPluginName}
	for _, opt := range opts {
		if opt != nil {
			opt(p)
		}
	}
	if p.name == "" {
		p.name = defaultPluginName
	}
	return p
}

// WithName overrides the plugin name.
func WithName(name string) Option {
	return func(p *Plugin) {
		if p == nil || name == "" {
			return
		}
		p.name = name
	}
}

// WithRequestBody enables raw request body logging.
func WithRequestBody() Option {
	return func(p *Plugin) {
		if p == nil {
			return
		}
		p.requestBody = true
	}
}

// WithResponseBody enables raw non-streaming response body logging.
func WithResponseBody() Option {
	return func(p *Plugin) {
		if p == nil {
			return
		}
		p.responseBody = true
	}
}

// WithRewrite200Error rewrites 200 OK JSON responses that contain a top-level
// non-null "error" field into HTTP 400 responses.
func WithRewrite200Error() Option {
	return func(p *Plugin) {
		if p == nil {
			return
		}
		p.rewrite200Error = true
	}
}

// Name implements plugin.Plugin.
func (p *Plugin) Name() string {
	if p == nil {
		return ""
	}
	return p.name
}

// Register implements plugin.Plugin.
func (p *Plugin) Register(r *pluginbase.Registry) {
	if p == nil || r == nil {
		return
	}
	r.BeforeModel(p.beforeModel)
}

func (p *Plugin) beforeModel(
	ctx context.Context,
	_ *modelplugin.BeforeModelArgs,
) (*modelplugin.BeforeModelResult, error) {
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil || inv.Model == nil {
		return nil, nil
	}
	switch inv.Model.(type) {
	case *openaimodel.Model:
		return &modelplugin.BeforeModelResult{
			Context: openaimodel.WithRequestOptions(
				ctx,
				openaiopt.WithMiddleware(p.openAIMiddleware()),
			),
		}, nil
	case *anthropicmodel.Model:
		return &modelplugin.BeforeModelResult{
			Context: anthropicmodel.WithRequestOptions(
				ctx,
				anthropicopt.WithMiddleware(p.anthropicMiddleware()),
			),
		}, nil
	default:
		return nil, nil
	}
}

func (p *Plugin) openAIMiddleware() openaiopt.Middleware {
	return func(
		req *http.Request,
		next openaiopt.MiddlewareNext,
	) (*http.Response, error) {
		return p.handle(req, next)
	}
}

func (p *Plugin) anthropicMiddleware() anthropicopt.Middleware {
	return func(
		req *http.Request,
		next anthropicopt.MiddlewareNext,
	) (*http.Response, error) {
		return p.handle(req, next)
	}
}

func (p *Plugin) handle(
	req *http.Request,
	next func(*http.Request) (*http.Response, error),
) (*http.Response, error) {
	if req == nil {
		return next(req)
	}
	logger.Debugf("httpdiag: -> %s %s", req.Method, req.URL.String())
	if p.requestBody {
		logRequestBody(req)
	}

	resp, err := next(req)
	if err != nil {
		logger.Debugf("httpdiag: <- %s %s err=%v", req.Method, req.URL.String(), err)
		return resp, err
	}
	if resp == nil {
		return nil, nil
	}

	bodyBytes, canInspectBody := maybeReadResponseBody(resp, p.responseBody || p.rewrite200Error)
	if p.rewrite200Error && canInspectBody && shouldRewriteErrorResponse(resp, bodyBytes) {
		logger.Debugf("httpdiag: 200-with-error detected, rewriting to 400: %s", prettyJSON(bodyBytes))
		resp = cloneResponseWithStatus(resp, http.StatusBadRequest, bodyBytes)
	}

	logger.Debugf("httpdiag: <- %s %s status=%d", req.Method, req.URL.String(), resp.StatusCode)
	if p.responseBody && canInspectBody {
		logger.Debugf("httpdiag: response body (status=%d):\n%s", resp.StatusCode, prettyJSON(bodyBytes))
	}
	return resp, nil
}

func logRequestBody(req *http.Request) {
	if req == nil || req.Body == nil {
		return
	}
	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil {
		return
	}
	req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	logger.Debugf("httpdiag: request body:\n%s", prettyJSON(bodyBytes))
}

func maybeReadResponseBody(resp *http.Response, needBody bool) ([]byte, bool) {
	if resp == nil || !needBody || resp.Body == nil || isStreamingResponse(resp) {
		return nil, false
	}
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, false
	}
	resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	return bodyBytes, true
}

func isStreamingResponse(resp *http.Response) bool {
	if resp == nil {
		return false
	}
	return strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream")
}

func shouldRewriteErrorResponse(resp *http.Response, bodyBytes []byte) bool {
	if resp == nil || resp.StatusCode != http.StatusOK || len(bodyBytes) == 0 {
		return false
	}
	var jsonResp map[string]any
	if err := json.Unmarshal(bodyBytes, &jsonResp); err != nil {
		return false
	}
	errorObj, ok := jsonResp["error"]
	return ok && errorObj != nil
}

func cloneResponseWithStatus(
	resp *http.Response,
	statusCode int,
	bodyBytes []byte,
) *http.Response {
	if resp == nil {
		return nil
	}
	status := http.StatusText(statusCode)
	if status == "" {
		status = "Unknown Status"
	}
	return &http.Response{
		Status:        fmt.Sprintf("%d %s", statusCode, status),
		StatusCode:    statusCode,
		Proto:         resp.Proto,
		ProtoMajor:    resp.ProtoMajor,
		ProtoMinor:    resp.ProtoMinor,
		Header:        resp.Header.Clone(),
		Body:          io.NopCloser(bytes.NewReader(bodyBytes)),
		ContentLength: int64(len(bodyBytes)),
		Request:       resp.Request,
	}
}

// prettyJSON tries to pretty-print raw bytes as indented JSON.
// Falls back to the raw string if the input is not valid JSON.
func prettyJSON(data []byte) string {
	var buf bytes.Buffer
	if err := json.Indent(&buf, data, "", "  "); err != nil {
		return string(data)
	}
	return buf.String()
}
