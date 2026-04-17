//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package mem0

import (
	"reflect"
	"testing"
)

func TestCloneMetadata_NilAndEmpty(t *testing.T) {
	if got := cloneMetadata(nil); got != nil {
		t.Fatalf("nil input: want nil, got %v", got)
	}
	if got := cloneMetadata(map[string]any{}); got != nil {
		t.Fatalf("empty input: want nil, got %v", got)
	}
}

func TestCloneMetadata_DeepCloneIsolatesNestedValues(t *testing.T) {
	nestedMap := map[string]any{"leaf": "original"}
	nestedSlice := []any{"a", "b"}
	original := map[string]any{
		"string":      "hello",
		"number":      float64(42),
		"nested_map":  nestedMap,
		"nested_list": nestedSlice,
	}

	cloned := cloneMetadata(original)
	if cloned == nil {
		t.Fatal("cloneMetadata returned nil for non-empty input")
	}

	// Outer map must be a distinct instance.
	if reflect.ValueOf(cloned).Pointer() == reflect.ValueOf(original).Pointer() {
		t.Fatal("outer map aliases the caller's map")
	}

	// Mutating the caller's nested map must not affect the clone.
	nestedMap["leaf"] = "mutated"
	clonedNested, ok := cloned["nested_map"].(map[string]any)
	if !ok {
		t.Fatalf("nested_map in clone has unexpected type %T", cloned["nested_map"])
	}
	if got := clonedNested["leaf"]; got != "original" {
		t.Fatalf("nested map aliased caller: clone[nested_map][leaf]=%v, want %q", got, "original")
	}

	// Mutating the caller's nested slice must not affect the clone.
	nestedSlice[0] = "mutated"
	clonedSlice, ok := cloned["nested_list"].([]any)
	if !ok {
		t.Fatalf("nested_list in clone has unexpected type %T", cloned["nested_list"])
	}
	if got := clonedSlice[0]; got != "a" {
		t.Fatalf("nested slice aliased caller: clone[nested_list][0]=%v, want %q", got, "a")
	}

	// Primitive fields must still round-trip.
	if cloned["string"] != "hello" {
		t.Fatalf("string field lost: got %v", cloned["string"])
	}
	if cloned["number"] != float64(42) {
		t.Fatalf("number field lost: got %v", cloned["number"])
	}
}

func TestCloneMetadata_NonSerializableInputReturnsNil(t *testing.T) {
	// Channels are not JSON-serializable; cloneMetadata must surface the
	// failure as a nil result rather than aliasing the caller's map.
	meta := map[string]any{"ch": make(chan int)}
	if got := cloneMetadata(meta); got != nil {
		t.Fatalf("non-serializable metadata: want nil, got %v", got)
	}
}
