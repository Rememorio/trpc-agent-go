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
