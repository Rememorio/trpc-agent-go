//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package octool

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBlocksSensitivePath_BlocksDotEnvAccess(t *testing.T) {
	t.Parallel()

	require.True(t, blocksSensitivePath(`python - <<'PY'
from pathlib import Path
print(Path("/tmp/.env.local").read_text())
PY`))
}

func TestBlocksSensitivePath_IgnoresPythonOsEnviron(t *testing.T) {
	t.Parallel()

	require.False(t, blocksSensitivePath(`python - <<'PY'
import os
print(os.environ.get("OPENCLAW_MEMORY_FILE"))
PY`))
}

func TestBlocksSensitiveEnv_BlocksPythonSensitiveVarRead(t *testing.T) {
	t.Parallel()

	require.True(t, blocksSensitiveEnv(`python - <<'PY'
import os
print(os.environ.get("OPENAI_API_KEY"))
PY`))
}

func TestBlocksSensitiveEnv_BlocksNodeSensitiveVarRead(t *testing.T) {
	t.Parallel()

	require.True(
		t,
		blocksSensitiveEnv(
			`node -e 'console.log(process.env.OPENAI_API_KEY)'`,
		),
	)
}

func TestChatCommandSafetyPolicy_AllowsPythonOsEnviron(t *testing.T) {
	t.Parallel()

	err := NewChatCommandSafetyPolicy()(context.Background(), CommandRequest{
		Command: `python - <<'PY'
import os
print(os.environ.get("OPENCLAW_MEMORY_FILE"))
PY`,
	})
	require.NoError(t, err)
}

func TestChatCommandSafetyPolicy_BlocksPythonSensitiveEnvRead(t *testing.T) {
	t.Parallel()

	err := NewChatCommandSafetyPolicy()(context.Background(), CommandRequest{
		Command: `python - <<'PY'
import os
print(os.environ.get("OPENAI_API_KEY"))
PY`,
	})
	require.ErrorContains(t, err, reasonSensitiveEnv)
}
