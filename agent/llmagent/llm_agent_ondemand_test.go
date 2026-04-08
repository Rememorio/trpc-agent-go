//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package llmagent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/processor"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

type onDemandSessionToolService struct {
	session.Service
}

func (s *onDemandSessionToolService) SearchEvents(
	context.Context,
	session.EventSearchRequest,
) ([]session.EventSearchResult, error) {
	return nil, nil
}

func (s *onDemandSessionToolService) GetEventWindow(
	context.Context,
	session.EventWindowRequest,
) (*session.EventWindow, error) {
	return &session.EventWindow{}, nil
}

func TestBuildRequestProcessors_OnDemandSessionWiring(t *testing.T) {
	opts := &Options{}
	WithEnableOnDemandSession(true)(opts)

	procs := buildRequestProcessors("tester", opts)
	var found bool
	for _, proc := range procs {
		if _, ok := proc.(*processor.OnDemandSessionRequestProcessor); ok {
			found = true
			break
		}
	}
	require.True(t, found)
}

func TestLLMAgent_OnDemandSessionTools_StaticAndInvocationAware(t *testing.T) {
	a := New("tester", WithEnableOnDemandSession(true))
	require.NotNil(t, findTool(a.Tools(), "session_search"))
	require.NotNil(t, findTool(a.Tools(), "session_load"))

	unsupportedInv := &agent.Invocation{
		Session:        session.NewSession("app", "user", "sess"),
		SessionService: sessioninmemory.NewSessionService(),
	}
	tools, _ := a.InvocationToolSurface(context.Background(), unsupportedInv)
	require.Nil(t, findTool(tools, "session_search"))
	require.Nil(t, findTool(tools, "session_load"))

	supportedInv := &agent.Invocation{
		Session: session.NewSession("app", "user", "sess"),
		SessionService: &onDemandSessionToolService{
			Service: sessioninmemory.NewSessionService(),
		},
	}
	tools, _ = a.InvocationToolSurface(context.Background(), supportedInv)
	require.NotNil(t, findTool(tools, "session_search"))
	require.NotNil(t, findTool(tools, "session_load"))
}
