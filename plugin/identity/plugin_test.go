//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package identity

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestPlugin_Name(t *testing.T) {
	p := NewPlugin(nil)
	assert.Equal(t, "identity", p.Name())

	p2 := NewNamedPlugin("my-auth", nil)
	assert.Equal(t, "my-auth", p2.Name())
}

func TestPlugin_ImplementsInterface(t *testing.T) {
	var _ plugin.Plugin = (*Plugin)(nil)
}

func TestIdentity_ContextRoundTrip(t *testing.T) {
	id := &Identity{UserID: "eve", Token: "t"}
	ctx := NewContext(context.Background(), id)
	got, ok := FromContext(ctx)
	require.True(t, ok)
	assert.Equal(t, "eve", got.UserID)

	_, ok = FromContext(context.Background())
	assert.False(t, ok)

	var nilCtx context.Context
	_, ok = FromContext(nilCtx)
	assert.False(t, ok)
}

func TestIdentity_HeadersAndEnvFromContext(t *testing.T) {
	id := &Identity{
		Headers: map[string]string{"Authorization": "Bearer tok"},
		EnvVars: map[string]string{"USER_ACCESS_TOKEN": "user-token"},
	}
	ctx := NewContext(context.Background(), id)

	headers, err := HeadersFromContext(ctx)
	require.NoError(t, err)
	require.Equal(t, "Bearer tok", headers["Authorization"])
	headers["Authorization"] = "mutated"
	require.Equal(t, "Bearer tok", id.Headers["Authorization"])

	env := EnvVarsFromContext(ctx)
	require.Equal(t, "user-token", env["USER_ACCESS_TOKEN"])
	env["USER_ACCESS_TOKEN"] = "mutated"
	require.Equal(t, "user-token", id.EnvVars["USER_ACCESS_TOKEN"])

	headers, err = HeadersFromContext(context.Background())
	require.NoError(t, err)
	require.Nil(t, headers)
	require.Nil(t, EnvVarsFromContext(context.Background()))
}

func TestPlugin_BeforeAgent_ResolvesIdentity(t *testing.T) {
	resolved := &Identity{
		UserID: "alice",
		Token:  "tok-123",
		Headers: map[string]string{
			"Authorization": "Bearer tok-123",
		},
		EnvVars: map[string]string{
			"BIZ_TOKEN": "tok-123",
		},
	}

	p := NewPlugin(ProviderFunc(func(ctx context.Context, uid, sid string) (*Identity, error) {
		assert.Equal(t, "alice", uid)
		assert.Equal(t, "sess-1", sid)
		return resolved, nil
	}))

	inv := &agent.Invocation{
		Session: &session.Session{
			UserID: "alice",
			ID:     "sess-1",
		},
	}

	_, err := p.beforeAgent(context.Background(), &agent.BeforeAgentArgs{
		Invocation: inv,
	})
	require.NoError(t, err)

	val, ok := inv.GetState(StateKey)
	require.True(t, ok)
	got := val.(*Identity)
	assert.Equal(t, "alice", got.UserID)
	assert.Equal(t, "tok-123", got.Token)
}

func TestPlugin_BeforeAgent_NilProvider(t *testing.T) {
	p := NewPlugin(nil)
	inv := &agent.Invocation{
		Session: &session.Session{UserID: "x", ID: "s"},
	}
	_, err := p.beforeAgent(context.Background(), &agent.BeforeAgentArgs{Invocation: inv})
	require.NoError(t, err)
}

func TestPlugin_BeforeAgent_NilArgs(t *testing.T) {
	p := NewPlugin(ProviderFunc(func(ctx context.Context, uid, sid string) (*Identity, error) {
		t.Fatal("should not be called")
		return nil, nil
	}))
	_, err := p.beforeAgent(context.Background(), nil)
	require.NoError(t, err)
}

func TestPlugin_BeforeTool_InjectsContext(t *testing.T) {
	id := &Identity{UserID: "bob", Token: "tok-456"}

	p := NewPlugin(ProviderFunc(func(ctx context.Context, uid, sid string) (*Identity, error) {
		return id, nil
	}))

	inv := &agent.Invocation{
		Session: &session.Session{UserID: "bob", ID: "s1"},
	}
	p.beforeAgent(context.Background(), &agent.BeforeAgentArgs{Invocation: inv})

	ctx := agent.NewInvocationContext(context.Background(), inv)

	result, err := p.beforeTool(ctx, &tool.BeforeToolArgs{
		ToolName:  "calculator",
		Arguments: []byte(`{"a":1}`),
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Context)

	got, ok := FromContext(result.Context)
	require.True(t, ok)
	assert.Equal(t, "bob", got.UserID)
}

func TestPlugin_BeforeTool_InjectsEnvVars(t *testing.T) {
	id := &Identity{
		UserID:  "charlie",
		EnvVars: map[string]string{"USER_ACCESS_TOKEN": "user-999", "BIZ_USER_ID": "charlie"},
	}

	p := NewPlugin(ProviderFunc(func(ctx context.Context, uid, sid string) (*Identity, error) {
		return id, nil
	}))

	inv := &agent.Invocation{
		Session: &session.Session{UserID: "charlie", ID: "s2"},
	}
	p.beforeAgent(context.Background(), &agent.BeforeAgentArgs{Invocation: inv})
	ctx := agent.NewInvocationContext(context.Background(), inv)

	result, err := p.beforeTool(ctx, &tool.BeforeToolArgs{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"ls -la","workdir":"/tmp"}`),
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.ModifiedArguments)

	var m map[string]any
	require.NoError(t, json.Unmarshal(result.ModifiedArguments, &m))

	env, ok := m["env"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "user-999", env["USER_ACCESS_TOKEN"])
	assert.Equal(t, "charlie", env["BIZ_USER_ID"])
	assert.Equal(t, "ls -la", m["command"])
}

func TestPlugin_BeforeTool_PreservesExistingEnvVars(t *testing.T) {
	id := &Identity{
		UserID:  "dave",
		EnvVars: map[string]string{"NEW_VAR": "new", "EXISTING": "override-attempt"},
	}

	p := NewPlugin(ProviderFunc(func(ctx context.Context, uid, sid string) (*Identity, error) {
		return id, nil
	}))

	inv := &agent.Invocation{
		Session: &session.Session{UserID: "dave", ID: "s3"},
	}
	p.beforeAgent(context.Background(), &agent.BeforeAgentArgs{Invocation: inv})
	ctx := agent.NewInvocationContext(context.Background(), inv)

	result, err := p.beforeTool(ctx, &tool.BeforeToolArgs{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"echo hi","env":{"EXISTING":"keep-me"}}`),
	})
	require.NoError(t, err)
	require.NotNil(t, result.ModifiedArguments)

	var m map[string]any
	require.NoError(t, json.Unmarshal(result.ModifiedArguments, &m))

	env := m["env"].(map[string]any)
	assert.Equal(t, "keep-me", env["EXISTING"])
	assert.Equal(t, "new", env["NEW_VAR"])
}

func TestPlugin_BeforeTool_InjectsEnvVarsForEnvCapableDeclaration(t *testing.T) {
	id := &Identity{
		UserID:  "erin",
		EnvVars: map[string]string{"USER_ACCESS_TOKEN": "user-abc"},
	}

	p := NewPlugin(ProviderFunc(func(ctx context.Context, uid, sid string) (*Identity, error) {
		return id, nil
	}))

	inv := &agent.Invocation{
		Session: &session.Session{UserID: "erin", ID: "s4"},
	}
	p.beforeAgent(context.Background(), &agent.BeforeAgentArgs{Invocation: inv})
	ctx := agent.NewInvocationContext(context.Background(), inv)

	result, err := p.beforeTool(ctx, &tool.BeforeToolArgs{
		ToolName:  "custom_runner",
		Arguments: []byte(`{"command":"whoami"}`),
		Declaration: &tool.Declaration{
			InputSchema: &tool.Schema{
				Type: "object",
				Properties: map[string]*tool.Schema{
					"env": {Type: "object"},
				},
			},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result.ModifiedArguments)

	var m map[string]any
	require.NoError(t, json.Unmarshal(result.ModifiedArguments, &m))
	env := m["env"].(map[string]any)
	require.Equal(t, "user-abc", env["USER_ACCESS_TOKEN"])
}

func TestPlugin_BeforeTool_InjectsEnvVarsForSkillRun(t *testing.T) {
	id := &Identity{
		UserID:  "grace",
		EnvVars: map[string]string{"USER_ACCESS_TOKEN": "user-skill"},
	}

	p := NewPlugin(ProviderFunc(func(ctx context.Context, uid, sid string) (*Identity, error) {
		return id, nil
	}))

	inv := &agent.Invocation{
		Session: &session.Session{UserID: "grace", ID: "s5"},
	}
	p.beforeAgent(context.Background(), &agent.BeforeAgentArgs{Invocation: inv})
	ctx := agent.NewInvocationContext(context.Background(), inv)

	result, err := p.beforeTool(ctx, &tool.BeforeToolArgs{
		ToolName:  "skill_run",
		Arguments: []byte(`{"skill":"deploy","command":"./run.sh"}`),
	})
	require.NoError(t, err)
	require.NotNil(t, result.ModifiedArguments)

	var m map[string]any
	require.NoError(t, json.Unmarshal(result.ModifiedArguments, &m))
	env := m["env"].(map[string]any)
	require.Equal(t, "user-skill", env["USER_ACCESS_TOKEN"])
}

func TestPlugin_BeforeTool_NoModificationForNonExecTools(t *testing.T) {
	id := &Identity{UserID: "eve", EnvVars: map[string]string{"TOK": "val"}}

	p := NewPlugin(ProviderFunc(func(ctx context.Context, uid, sid string) (*Identity, error) {
		return id, nil
	}))

	inv := &agent.Invocation{
		Session: &session.Session{UserID: "eve", ID: "s4"},
	}
	p.beforeAgent(context.Background(), &agent.BeforeAgentArgs{Invocation: inv})
	ctx := agent.NewInvocationContext(context.Background(), inv)

	result, err := p.beforeTool(ctx, &tool.BeforeToolArgs{
		ToolName:  "calculator",
		Arguments: []byte(`{"a":1}`),
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Nil(t, result.ModifiedArguments)
}

func TestPlugin_BeforeTool_ArgInjection(t *testing.T) {
	id := &Identity{UserID: "frank", Token: "t-frank", Signature: "sig-frank"}

	p := NewPlugin(ProviderFunc(func(ctx context.Context, uid, sid string) (*Identity, error) {
		return id, nil
	}), WithArgInjection(true), WithEnvInjection(false))

	inv := &agent.Invocation{
		Session: &session.Session{UserID: "frank", ID: "s5"},
	}
	p.beforeAgent(context.Background(), &agent.BeforeAgentArgs{Invocation: inv})
	ctx := agent.NewInvocationContext(context.Background(), inv)

	result, err := p.beforeTool(ctx, &tool.BeforeToolArgs{
		ToolName:  "my_custom_tool",
		Arguments: []byte(`{"query":"hello"}`),
	})
	require.NoError(t, err)
	require.NotNil(t, result.ModifiedArguments)

	var m map[string]any
	require.NoError(t, json.Unmarshal(result.ModifiedArguments, &m))

	idMap, ok := m["_identity"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "frank", idMap["user_id"])
	assert.Equal(t, "t-frank", idMap["token"])
	assert.Equal(t, "sig-frank", idMap["signature"])
	assert.Equal(t, "hello", m["query"])
}

func TestPlugin_BeforeTool_EnvAndArgInjectionCompose(t *testing.T) {
	id := &Identity{
		UserID:    "harry",
		Token:     "tok-harry",
		Signature: "sig-harry",
		EnvVars:   map[string]string{"USER_ACCESS_TOKEN": "user-harry"},
	}

	p := NewPlugin(ProviderFunc(func(ctx context.Context, uid, sid string) (*Identity, error) {
		return id, nil
	}), WithArgInjection(true))

	inv := &agent.Invocation{
		Session: &session.Session{UserID: "harry", ID: "s6"},
	}
	p.beforeAgent(context.Background(), &agent.BeforeAgentArgs{Invocation: inv})
	ctx := agent.NewInvocationContext(context.Background(), inv)

	result, err := p.beforeTool(ctx, &tool.BeforeToolArgs{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"env"}`),
	})
	require.NoError(t, err)
	require.NotNil(t, result.ModifiedArguments)

	var m map[string]any
	require.NoError(t, json.Unmarshal(result.ModifiedArguments, &m))
	env := m["env"].(map[string]any)
	require.Equal(t, "user-harry", env["USER_ACCESS_TOKEN"])
	idMap := m["_identity"].(map[string]any)
	require.Equal(t, "harry", idMap["user_id"])
	require.Equal(t, "tok-harry", idMap["token"])
}

func TestPlugin_BeforeTool_InjectsEnvVarsIntoEmptyArguments(t *testing.T) {
	id := &Identity{
		UserID:  "ivy",
		EnvVars: map[string]string{"USER_ACCESS_TOKEN": "user-empty"},
	}

	p := NewPlugin(ProviderFunc(func(ctx context.Context, uid, sid string) (*Identity, error) {
		return id, nil
	}))

	inv := &agent.Invocation{
		Session: &session.Session{UserID: "ivy", ID: "s7"},
	}
	p.beforeAgent(context.Background(), &agent.BeforeAgentArgs{Invocation: inv})
	ctx := agent.NewInvocationContext(context.Background(), inv)

	result, err := p.beforeTool(ctx, &tool.BeforeToolArgs{
		ToolName:  "custom_runner",
		Arguments: nil,
		Declaration: &tool.Declaration{
			InputSchema: &tool.Schema{
				Type: "object",
				Properties: map[string]*tool.Schema{
					"env": {Type: "object"},
				},
			},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.ModifiedArguments)

	var m map[string]any
	require.NoError(t, json.Unmarshal(result.ModifiedArguments, &m))
	env := m["env"].(map[string]any)
	require.Equal(t, "user-empty", env["USER_ACCESS_TOKEN"])
}

func TestPlugin_BeforeTool_ArgInjectionSupportsNullArguments(t *testing.T) {
	id := &Identity{UserID: "jane", Token: "tok-jane", Signature: "sig-jane"}

	p := NewPlugin(ProviderFunc(func(ctx context.Context, uid, sid string) (*Identity, error) {
		return id, nil
	}), WithArgInjection(true), WithEnvInjection(false))

	inv := &agent.Invocation{
		Session: &session.Session{UserID: "jane", ID: "s8"},
	}
	p.beforeAgent(context.Background(), &agent.BeforeAgentArgs{Invocation: inv})
	ctx := agent.NewInvocationContext(context.Background(), inv)

	result, err := p.beforeTool(ctx, &tool.BeforeToolArgs{
		ToolName:  "my_custom_tool",
		Arguments: []byte(`null`),
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.ModifiedArguments)

	var m map[string]any
	require.NoError(t, json.Unmarshal(result.ModifiedArguments, &m))
	idMap := m["_identity"].(map[string]any)
	require.Equal(t, "jane", idMap["user_id"])
	require.Equal(t, "tok-jane", idMap["token"])
	require.Equal(t, "sig-jane", idMap["signature"])
}

func TestPlugin_BeforeTool_NoIdentityInState(t *testing.T) {
	p := NewPlugin(ProviderFunc(func(ctx context.Context, uid, sid string) (*Identity, error) {
		return nil, nil
	}))

	inv := &agent.Invocation{
		Session: &session.Session{UserID: "ghost", ID: "s6"},
	}
	p.beforeAgent(context.Background(), &agent.BeforeAgentArgs{Invocation: inv})
	ctx := agent.NewInvocationContext(context.Background(), inv)

	result, err := p.beforeTool(ctx, &tool.BeforeToolArgs{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"ls"}`),
	})
	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestProviderFunc(t *testing.T) {
	called := false
	f := ProviderFunc(func(ctx context.Context, uid, sid string) (*Identity, error) {
		called = true
		return &Identity{UserID: uid}, nil
	})
	id, err := f.Resolve(context.Background(), "u", "s")
	require.NoError(t, err)
	assert.True(t, called)
	assert.Equal(t, "u", id.UserID)
}
