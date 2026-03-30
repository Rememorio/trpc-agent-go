//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package mem0

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestSessionScan_ReadWriteLastExtractAt(t *testing.T) {
	sess := session.NewSession(testAppID, testUserID, "sid")
	assert.True(t, readLastExtractAt(sess).IsZero())

	ts := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	writeLastExtractAt(sess, ts)
	got := readLastExtractAt(sess)
	assert.Equal(t, ts, got)

	sess.SetState(memory.SessionStateKeyAutoMemoryLastExtractAt, []byte("bad"))
	assert.True(t, readLastExtractAt(sess).IsZero())
}

func TestSessionScan_ScanDeltaSince(t *testing.T) {
	ts1 := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	ts2 := ts1.Add(2 * time.Second)

	userMsg := model.Message{Role: model.RoleUser, Content: "hi"}
	assistantMsg := model.Message{Role: model.RoleAssistant, Content: "hello"}
	toolMsg := model.Message{Role: model.RoleTool, Content: "skip"}

	e1 := event.Event{Timestamp: ts1, Response: &model.Response{Choices: []model.Choice{{Message: userMsg}}}}
	e2 := event.Event{Timestamp: ts2, Response: &model.Response{Choices: []model.Choice{{Message: assistantMsg}, {Message: toolMsg}}}}

	sess := session.NewSession(testAppID, testUserID, "sid", session.WithSessionEvents([]event.Event{e1, e2}))
	latest, msgs := scanDeltaSince(sess, time.Time{})
	require.Equal(t, ts2, latest)
	require.Len(t, msgs, 2)
	assert.Equal(t, model.RoleUser, msgs[0].Role)
	assert.Equal(t, model.RoleAssistant, msgs[1].Role)

	latest, msgs = scanDeltaSince(sess, ts2)
	assert.True(t, latest.IsZero())
	assert.Nil(t, msgs)
}

func TestSessionScan_WriteLastExtractAt_NilSession(t *testing.T) {
	writeLastExtractAt(nil, time.Now())
}

func TestSessionScan_ReadLastExtractAt_NilSession(t *testing.T) {
	ts := readLastExtractAt(nil)
	assert.True(t, ts.IsZero())
}

func TestSessionScan_ScanDeltaSince_NilSession(t *testing.T) {
	latest, msgs := scanDeltaSince(nil, time.Time{})
	assert.True(t, latest.IsZero())
	assert.Nil(t, msgs)
}

func TestSessionScan_ScanDeltaSince_ToolCalls(t *testing.T) {
	ts := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	toolCallMsg := model.Message{
		Role:    model.RoleAssistant,
		Content: "calling tool",
		ToolCalls: []model.ToolCall{
			{ID: "tc1", Function: model.FunctionDefinitionParam{Name: "fn", Arguments: json.RawMessage("{}")}},
		},
	}
	toolRespMsg := model.Message{Role: model.RoleTool, Content: "result", ToolID: "tc1"}
	emptyMsg := model.Message{Role: model.RoleUser}
	validMsg := model.Message{Role: model.RoleUser, Content: "hello"}

	events := []event.Event{
		{Timestamp: ts, Response: &model.Response{Choices: []model.Choice{
			{Message: toolCallMsg}, {Message: toolRespMsg}, {Message: emptyMsg}, {Message: validMsg},
		}}},
	}
	sess := session.NewSession(testAppID, testUserID, "sid",
		session.WithSessionEvents(events))
	latest, msgs := scanDeltaSince(sess, time.Time{})
	assert.Equal(t, ts, latest)
	require.Len(t, msgs, 1)
	assert.Equal(t, "hello", msgs[0].Content)
}

func TestSessionScan_ScanDeltaSince_NilResponse(t *testing.T) {
	ts := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	events := []event.Event{
		{Timestamp: ts, Response: nil},
	}
	sess := session.NewSession(testAppID, testUserID, "sid",
		session.WithSessionEvents(events))
	latest, msgs := scanDeltaSince(sess, time.Time{})
	assert.Equal(t, ts, latest)
	assert.Empty(t, msgs)
}

func TestSessionScan_ScanDeltaSince_SystemRoleSkipped(t *testing.T) {
	ts := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	sysMsg := model.Message{Role: model.RoleSystem, Content: "you are helpful"}
	events := []event.Event{
		{Timestamp: ts, Response: &model.Response{Choices: []model.Choice{{Message: sysMsg}}}},
	}
	sess := session.NewSession(testAppID, testUserID, "sid",
		session.WithSessionEvents(events))
	_, msgs := scanDeltaSince(sess, time.Time{})
	assert.Empty(t, msgs)
}
