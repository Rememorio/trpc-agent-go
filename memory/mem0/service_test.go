//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package mem0

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestIngestSession_InvalidUserKeyReturnsError(t *testing.T) {
	svc, err := NewService(WithAPIKey("test-key"))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(func() {
		_ = svc.Close()
	})

	testCases := []struct {
		name string
		sess *session.Session
	}{
		{
			name: "empty_app_name",
			sess: &session.Session{AppName: "", UserID: "user-1", ID: "sess-1"},
		},
		{
			name: "empty_user_id",
			sess: &session.Session{AppName: "app-1", UserID: "", ID: "sess-1"},
		},
		{
			name: "both_empty",
			sess: &session.Session{AppName: "", UserID: "", ID: "sess-1"},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if err := svc.IngestSession(context.Background(), tc.sess); err == nil {
				t.Fatal("IngestSession: want error for invalid user key, got nil")
			}
		})
	}
}

func TestIngestSession_NilSessionReturnsNil(t *testing.T) {
	svc, err := NewService(WithAPIKey("test-key"))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(func() {
		_ = svc.Close()
	})
	if err := svc.IngestSession(context.Background(), nil); err != nil {
		t.Fatalf("IngestSession(nil session): want nil, got %v", err)
	}
}
