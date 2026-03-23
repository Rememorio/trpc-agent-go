//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package memorydocs

import "strings"

func DefaultMemoryTemplate() string {
	return strings.Join([]string{
		"# Memory",
		"",
		"This file stores stable, low-volume memory about the user.",
		"",
		"The agent may update this file only when all conditions hold:",
		"- The information is likely to matter in future sessions.",
		"- The information is stable, not task-local noise.",
		"- The information can be written as a short bullet.",
		"- The information does not contain secrets.",
		"",
		"Do not store:",
		"- Secrets, credentials, or private tokens.",
		"- Large conversation summaries.",
		"- One-off debugging details.",
		"",
		"## Long-term facts",
		"",
		"## Preferences",
		"",
		"## Repeated working style",
		"",
	}, "\n")
}
