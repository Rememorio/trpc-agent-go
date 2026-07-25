//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package extractor

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOperationPublicShapeCompatibility(t *testing.T) {
	operationType := reflect.TypeOf(Operation{})
	require.Equal(t, 8, operationType.NumField())
	for i := 0; i < operationType.NumField(); i++ {
		assert.True(t, operationType.Field(i).IsExported())
	}
}
