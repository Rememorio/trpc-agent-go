//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package model

import (
	"bytes"
	"encoding/json"
)

// ProviderData stores provider-owned, versioned response metadata that must
// survive serialization and conversation replay. Keys should be stable
// provider namespaces, for example "openai.responses".
type ProviderData map[string]json.RawMessage

// Clone returns a deep copy suitable for attaching provider metadata to a
// derived message or tool result.
func (data ProviderData) Clone() ProviderData {
	return cloneProviderData(data)
}

// ProviderOptions stores provider-owned request configuration. Model adapters
// interpret their own namespace and must ignore namespaces they do not own.
// ProviderOptions is not serialized into provider request bodies directly.
type ProviderOptions map[string]json.RawMessage

// Clone returns a deep copy of provider request options.
func (options ProviderOptions) Clone() ProviderOptions {
	return cloneProviderOptions(options)
}

func cloneProviderData(data ProviderData) ProviderData {
	if data == nil {
		return nil
	}
	cloned := make(ProviderData, len(data))
	for key, value := range data {
		cloned[key] = bytes.Clone(value)
	}
	return cloned
}

func cloneProviderOptions(options ProviderOptions) ProviderOptions {
	if options == nil {
		return nil
	}
	cloned := make(ProviderOptions, len(options))
	for key, value := range options {
		cloned[key] = bytes.Clone(value)
	}
	return cloned
}

func providerDataEqual(a, b ProviderData) bool {
	if len(a) != len(b) {
		return false
	}
	for key, aValue := range a {
		bValue, ok := b[key]
		if !ok || !rawJSONEqual(aValue, bValue) {
			return false
		}
	}
	return true
}

func rawJSONEqual(a, b json.RawMessage) bool {
	if bytes.Equal(a, b) {
		return true
	}
	var aValue any
	if err := json.Unmarshal(a, &aValue); err != nil {
		return false
	}
	var bValue any
	if err := json.Unmarshal(b, &bValue); err != nil {
		return false
	}
	aCanonical, err := json.Marshal(aValue)
	if err != nil {
		return false
	}
	bCanonical, err := json.Marshal(bValue)
	if err != nil {
		return false
	}
	return bytes.Equal(aCanonical, bCanonical)
}
