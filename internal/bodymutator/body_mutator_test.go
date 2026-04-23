// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package bodymutator

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/json"
)

func ptrBool(b bool) *bool {
	return &b
}

func TestBodyMutator_Mutate_Set(t *testing.T) {
	bodyMutations := &filterapi.HTTPBodyMutation{
		Set: []filterapi.HTTPBodyField{
			{Path: "service_tier", Value: "\"scale\""},
			{Path: "max_tokens", Value: "100"},
			{Path: "temperature", Value: "0.7"},
		},
	}

	originalBody := []byte(`{"model": "gpt-4", "service_tier": "default", "messages": []}`)
	mutator := NewBodyMutator(bodyMutations, originalBody)

	requestBody := []byte(`{"model": "gpt-4", "service_tier": "default", "messages": []}`)

	mutatedBody, err := mutator.Mutate(requestBody)
	require.NoError(t, err)

	var result map[string]interface{}
	err = json.Unmarshal(mutatedBody, &result)
	require.NoError(t, err)

	require.Equal(t, "scale", result["service_tier"])
	require.Equal(t, float64(100), result["max_tokens"])
	require.Equal(t, 0.7, result["temperature"])
	require.Equal(t, "gpt-4", result["model"])
}

func TestBodyMutator_Mutate_Remove(t *testing.T) {
	bodyMutations := &filterapi.HTTPBodyMutation{
		Remove: []string{"service_tier", "internal_flag"},
	}

	originalBody := []byte(`{"model": "gpt-4", "service_tier": "default", "internal_flag": true, "messages": []}`)
	mutator := NewBodyMutator(bodyMutations, originalBody)

	requestBody := []byte(`{"model": "gpt-4", "service_tier": "default", "internal_flag": true, "messages": []}`)

	mutatedBody, err := mutator.Mutate(requestBody)
	require.NoError(t, err)

	var result map[string]interface{}
	err = json.Unmarshal(mutatedBody, &result)
	require.NoError(t, err)

	require.NotContains(t, result, "service_tier")
	require.NotContains(t, result, "internal_flag")
	require.Equal(t, "gpt-4", result["model"])
	require.Contains(t, result, "messages")
}

func TestBodyMutator_Mutate_SetAndRemove(t *testing.T) {
	bodyMutations := &filterapi.HTTPBodyMutation{
		Set: []filterapi.HTTPBodyField{
			{Path: "service_tier", Value: "\"premium\""},
			{Path: "new_field", Value: "\"added\""},
		},
		Remove: []string{"internal_flag"},
	}

	originalBody := []byte(`{"model": "gpt-4", "service_tier": "default", "internal_flag": true}`)
	mutator := NewBodyMutator(bodyMutations, originalBody)

	requestBody := []byte(`{"model": "gpt-4", "service_tier": "default", "internal_flag": true}`)

	mutatedBody, err := mutator.Mutate(requestBody)
	require.NoError(t, err)

	var result map[string]interface{}
	err = json.Unmarshal(mutatedBody, &result)
	require.NoError(t, err)

	require.Equal(t, "premium", result["service_tier"])
	require.Equal(t, "added", result["new_field"])
	require.NotContains(t, result, "internal_flag")
	require.Equal(t, "gpt-4", result["model"])
}

func TestBodyMutator_Mutate_ComplexValues(t *testing.T) {
	bodyMutations := &filterapi.HTTPBodyMutation{
		Set: []filterapi.HTTPBodyField{
			{Path: "object_field", Value: `{"nested": "value", "number": 42}`},
			{Path: "array_field", Value: `[1, 2, 3]`},
			{Path: "null_field", Value: "null"},
			{Path: "boolean_field", Value: "true"},
		},
	}

	originalBody := []byte(`{"model": "gpt-4"}`)
	mutator := NewBodyMutator(bodyMutations, originalBody)

	requestBody := []byte(`{"model": "gpt-4"}`)

	mutatedBody, err := mutator.Mutate(requestBody)
	require.NoError(t, err)

	var result map[string]interface{}
	err = json.Unmarshal(mutatedBody, &result)
	require.NoError(t, err)

	require.Equal(t, "gpt-4", result["model"])

	// Check object field
	objectField, ok := result["object_field"].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, "value", objectField["nested"])
	require.Equal(t, float64(42), objectField["number"])

	// Check array field
	arrayField, ok := result["array_field"].([]interface{})
	require.True(t, ok)
	require.Equal(t, []interface{}{float64(1), float64(2), float64(3)}, arrayField)

	// Check null field
	require.Nil(t, result["null_field"])

	// Check boolean field
	require.Equal(t, true, result["boolean_field"])
}

func TestBodyMutator_Mutate_NoMutations(t *testing.T) {
	mutator := NewBodyMutator(nil, nil)

	requestBody := []byte(`{"model": "gpt-4", "service_tier": "default"}`)

	mutatedBody, err := mutator.Mutate(requestBody)
	require.NoError(t, err)

	require.Equal(t, requestBody, mutatedBody)
}

func TestBodyMutator_Mutate_InvalidJSON(t *testing.T) {
	bodyMutations := &filterapi.HTTPBodyMutation{
		Set: []filterapi.HTTPBodyField{
			{Path: "service_tier", Value: "premium"},
		},
	}

	originalBody := []byte(`{"model": "gpt-4"}`)
	mutator := NewBodyMutator(bodyMutations, originalBody)

	invalidRequestBody := []byte(`{invalid json}`)

	// sjson is more graceful and can handle malformed JSON
	mutatedBody, err := mutator.Mutate(invalidRequestBody)
	require.NoError(t, err)
	require.NotNil(t, mutatedBody)

	// The result should have the mutation applied
	require.Contains(t, string(mutatedBody), "service_tier")
	require.Contains(t, string(mutatedBody), "premium")
}

func TestBodyMutator_Mutate_InvalidJSONValue(t *testing.T) {
	bodyMutations := &filterapi.HTTPBodyMutation{
		Set: []filterapi.HTTPBodyField{
			{Path: "service_tier", Value: "not valid json but will be treated as string"},
			{Path: "valid_field", Value: "\"valid\""},
		},
	}

	originalBody := []byte(`{"model": "gpt-4"}`)
	mutator := NewBodyMutator(bodyMutations, originalBody)

	requestBody := []byte(`{"model": "gpt-4"}`)

	mutatedBody, err := mutator.Mutate(requestBody)
	require.NoError(t, err)

	var result map[string]interface{}
	err = json.Unmarshal(mutatedBody, &result)
	require.NoError(t, err)

	// Invalid JSON values should be treated as strings
	require.Equal(t, "not valid json but will be treated as string", result["service_tier"])
	require.Equal(t, "valid", result["valid_field"])
}

// Tests for new merge functionality

func TestBodyMutator_Mutate_MergeExtraBody(t *testing.T) {
	bodyMutations := &filterapi.HTTPBodyMutation{
		Set: []filterapi.HTTPBodyField{
			{
				Path:  "extra_body.cache_salt",
				Value: "\"user123salt\"",
				Merge: ptrBool(true),
			},
		},
	}

	mutator := NewBodyMutator(bodyMutations, nil)

	// Input has existing extra_body with temperature
	requestBody := []byte(`{"model": "gpt-4", "extra_body": {"temperature": 0.7}}`)

	mutatedBody, err := mutator.Mutate(requestBody)
	require.NoError(t, err)

	var result map[string]interface{}
	err = json.Unmarshal(mutatedBody, &result)
	require.NoError(t, err)

	extraBody := result["extra_body"].(map[string]interface{})

	// Original temperature should be preserved
	require.Equal(t, 0.7, extraBody["temperature"])

	// New cache_salt should be added
	require.Equal(t, "user123salt", extraBody["cache_salt"])
}

func TestBodyMutator_Mutate_MergeNestedPath(t *testing.T) {
	bodyMutations := &filterapi.HTTPBodyMutation{
		Set: []filterapi.HTTPBodyField{
			{
				Path:  "metadata.cache_salt",
				Value: "\"test-salt\"",
				Merge: ptrBool(true),
			},
		},
	}

	mutator := NewBodyMutator(bodyMutations, nil)

	// Input has existing metadata
	requestBody := []byte(`{"model": "gpt-4", "metadata": {"owner": "test"}}`)

	mutatedBody, err := mutator.Mutate(requestBody)
	require.NoError(t, err)

	var result map[string]interface{}
	err = json.Unmarshal(mutatedBody, &result)
	require.NoError(t, err)

	metadata := result["metadata"].(map[string]interface{})

	// Original owner should be preserved
	require.Equal(t, "test", metadata["owner"])

	// New cache_salt should be added
	require.Equal(t, "test-salt", metadata["cache_salt"])
}

func TestBodyMutator_Mutate_MergeNonExistentPath(t *testing.T) {
	bodyMutations := &filterapi.HTTPBodyMutation{
		Set: []filterapi.HTTPBodyField{
			{
				Path:  "extra_body.cache_salt",
				Value: "\"new-salt\"",
				Merge: ptrBool(true),
			},
		},
	}

	mutator := NewBodyMutator(bodyMutations, nil)

	// Input doesn't have extra_body
	requestBody := []byte(`{"model": "gpt-4"}`)

	mutatedBody, err := mutator.Mutate(requestBody)
	require.NoError(t, err)

	var result map[string]interface{}
	err = json.Unmarshal(mutatedBody, &result)
	require.NoError(t, err)

	extraBody := result["extra_body"].(map[string]interface{})
	require.Equal(t, "new-salt", extraBody["cache_salt"])
}

func TestBodyMutator_Mutate_MergeReplaceExistingValue(t *testing.T) {
	bodyMutations := &filterapi.HTTPBodyMutation{
		Set: []filterapi.HTTPBodyField{
			{
				Path:  "extra_body.cache_salt",
				Value: "\"updated-salt\"",
				Merge: ptrBool(true),
			},
		},
	}

	mutator := NewBodyMutator(bodyMutations, nil)

	// Input has existing cache_salt
	requestBody := []byte(`{"model": "gpt-4", "extra_body": {"cache_salt": "old-salt", "temperature": 0.5}}`)

	mutatedBody, err := mutator.Mutate(requestBody)
	require.NoError(t, err)

	var result map[string]interface{}
	err = json.Unmarshal(mutatedBody, &result)
	require.NoError(t, err)

	extraBody := result["extra_body"].(map[string]interface{})

	// cache_salt should be updated
	require.Equal(t, "updated-salt", extraBody["cache_salt"])

	// temperature should be preserved
	require.Equal(t, 0.5, extraBody["temperature"])
}

func TestBodyMutator_Mutate_NoMergeReplacesExisting(t *testing.T) {
	bodyMutations := &filterapi.HTTPBodyMutation{
		Set: []filterapi.HTTPBodyField{
			{
				Path:  "extra_body",
				Value: `{"cache_salt": "replacement"}`,
				Merge: ptrBool(false), // No merge, replaces entire object
			},
		},
	}

	mutator := NewBodyMutator(bodyMutations, nil)

	// Input has existing extra_body with temperature
	requestBody := []byte(`{"model": "gpt-4", "extra_body": {"temperature": 0.7}}`)

	mutatedBody, err := mutator.Mutate(requestBody)
	require.NoError(t, err)

	var result map[string]interface{}
	err = json.Unmarshal(mutatedBody, &result)
	require.NoError(t, err)

	extraBody := result["extra_body"].(map[string]interface{})

	// Only cache_salt exists (original temperature lost because no merge)
	require.Len(t, extraBody, 1)
	require.Equal(t, "replacement", extraBody["cache_salt"])
	require.NotContains(t, extraBody, "temperature")
}

// Tests for dynamic value from header

func TestBodyMutator_MutateWithHeaders_ValueFromHeader(t *testing.T) {
	bodyMutations := &filterapi.HTTPBodyMutation{
		Set: []filterapi.HTTPBodyField{
			{
				Path: "extra_body.user",
				ValueFrom: &filterapi.ValueFrom{
					HeaderName: "x-user-id",
				},
			},
		},
	}

	mutator := NewBodyMutator(bodyMutations, nil)

	requestBody := []byte(`{"model": "gpt-4"}`)
	headers := map[string]string{
		"x-user-id": "test-user-123",
	}

	mutatedBody, err := mutator.MutateWithHeaders(requestBody, headers)
	require.NoError(t, err)

	var result map[string]interface{}
	err = json.Unmarshal(mutatedBody, &result)
	require.NoError(t, err)

	extraBody := result["extra_body"].(map[string]interface{})
	require.Equal(t, "test-user-123", extraBody["user"])
}

func TestBodyMutator_MutateWithHeaders_ValueFromHeaderSHA256(t *testing.T) {
	bodyMutations := &filterapi.HTTPBodyMutation{
		Set: []filterapi.HTTPBodyField{
			{
				Path: "extra_body.cache_salt",
				ValueFrom: &filterapi.ValueFrom{
					HeaderName: "x-user-id",
					Hash:       "sha256",
					Encoding:   "base64",
				},
			},
		},
	}

	mutator := NewBodyMutator(bodyMutations, nil)

	requestBody := []byte(`{"model": "gpt-4"}`)
	headers := map[string]string{
		"x-user-id": "test-user-123",
	}

	mutatedBody, err := mutator.MutateWithHeaders(requestBody, headers)
	require.NoError(t, err)

	var result map[string]interface{}
	err = json.Unmarshal(mutatedBody, &result)
	require.NoError(t, err)

	extraBody := result["extra_body"].(map[string]interface{})
	cacheSalt, ok := extraBody["cache_salt"].(string)
	require.True(t, ok, "cache_salt should be a string")

	// Verify it's valid base64 (SHA-256 hash of "test-user-123" is 32 bytes = 44 base64 chars)
	require.Len(t, cacheSalt, 44, "cache_salt should be base64 encoded SHA-256 hash")

	// Verify the hash is correct
	expectedHash := sha256.Sum256([]byte("test-user-123"))
	expectedB64 := base64.StdEncoding.EncodeToString(expectedHash[:])
	require.Equal(t, expectedB64, cacheSalt)
}

func TestBodyMutator_MutateWithHeaders_ValueFromHeaderMissing(t *testing.T) {
	bodyMutations := &filterapi.HTTPBodyMutation{
		Set: []filterapi.HTTPBodyField{
			{
				Path: "extra_body.cache_salt",
				ValueFrom: &filterapi.ValueFrom{
					HeaderName: "x-user-id",
					Hash:       "sha256",
					Encoding:   "base64",
				},
			},
		},
	}

	mutator := NewBodyMutator(bodyMutations, nil)

	requestBody := []byte(`{"model": "gpt-4"}`)
	// No x-user-id header
	headers := map[string]string{}

	mutatedBody, err := mutator.MutateWithHeaders(requestBody, headers)
	require.NoError(t, err)

	var result map[string]interface{}
	err = json.Unmarshal(mutatedBody, &result)
	require.NoError(t, err)

	// extra_body should not exist because header was missing
	_, exists := result["extra_body"]
	require.False(t, exists, "extra_body should not be created when header is missing")
}

func TestBodyMutator_MutateWithHeaders_ValueFromAndMerge(t *testing.T) {
	bodyMutations := &filterapi.HTTPBodyMutation{
		Set: []filterapi.HTTPBodyField{
			{
				Path: "extra_body.cache_salt",
				ValueFrom: &filterapi.ValueFrom{
					HeaderName: "x-user-id",
					Hash:       "sha256",
					Encoding:   "base64",
				},
				Merge: ptrBool(true),
			},
		},
	}

	mutator := NewBodyMutator(bodyMutations, nil)

	// Input has existing extra_body with temperature
	requestBody := []byte(`{"model": "gpt-4", "extra_body": {"temperature": 0.7}}`)
	headers := map[string]string{
		"x-user-id": "user-abc",
	}

	mutatedBody, err := mutator.MutateWithHeaders(requestBody, headers)
	require.NoError(t, err)

	var result map[string]interface{}
	err = json.Unmarshal(mutatedBody, &result)
	require.NoError(t, err)

	extraBody := result["extra_body"].(map[string]interface{})

	// Original temperature should be preserved
	require.Equal(t, 0.7, extraBody["temperature"])

	// cache_salt should be added
	cacheSalt, ok := extraBody["cache_salt"].(string)
	require.True(t, ok)
	require.Len(t, cacheSalt, 44) // base64 encoded SHA-256
}

func TestBodyMutator_MutateWithHeaders_NilHeaders(t *testing.T) {
	bodyMutations := &filterapi.HTTPBodyMutation{
		Set: []filterapi.HTTPBodyField{
			{
				Path: "extra_body.user",
				ValueFrom: &filterapi.ValueFrom{
					HeaderName: "x-user-id",
				},
			},
		},
	}

	mutator := NewBodyMutator(bodyMutations, nil)

	requestBody := []byte(`{"model": "gpt-4"}`)

	// Pass nil headers
	mutatedBody, err := mutator.MutateWithHeaders(requestBody, nil)
	require.NoError(t, err)

	var result map[string]interface{}
	err = json.Unmarshal(mutatedBody, &result)
	require.NoError(t, err)

	// extra_body should not exist because headers is nil
	_, exists := result["extra_body"]
	require.False(t, exists, "extra_body should not be created when headers is nil")
}

func TestBodyMutator_Mutate_ValueFromTakesPrecedenceOverValue(t *testing.T) {
	// When both Value and ValueFrom are set, ValueFrom takes precedence
	// This is the expected behavior since the user explicitly configured ValueFrom
	bodyMutations := &filterapi.HTTPBodyMutation{
		Set: []filterapi.HTTPBodyField{
			{
				Path:  "extra_body.user",
				Value: "\"static-user\"",
				ValueFrom: &filterapi.ValueFrom{
					HeaderName: "x-user-id", // This takes precedence when set
				},
			},
		},
	}

	mutator := NewBodyMutator(bodyMutations, nil)

	requestBody := []byte(`{"model": "gpt-4"}`)
	headers := map[string]string{
		"x-user-id": "header-user",
	}

	mutatedBody, err := mutator.MutateWithHeaders(requestBody, headers)
	require.NoError(t, err)

	var result map[string]interface{}
	err = json.Unmarshal(mutatedBody, &result)
	require.NoError(t, err)

	extraBody := result["extra_body"].(map[string]interface{})
	// ValueFrom takes precedence when both are set
	require.Equal(t, "header-user", extraBody["user"])
}

// Integration-style test for cache_salt use case
func TestBodyMutator_CacheSaltUseCase(t *testing.T) {
	// Simulate the actual configuration that would be used for cache_salt
	bodyMutations := &filterapi.HTTPBodyMutation{
		Set: []filterapi.HTTPBodyField{
			{
				Path: "extra_body.cache_salt",
				ValueFrom: &filterapi.ValueFrom{
					HeaderName: "x-user-id",
					Hash:       "sha256",
					Encoding:   "base64",
				},
				Merge: ptrBool(true),
			},
		},
	}

	mutator := NewBodyMutator(bodyMutations, nil)

	// User A's request with existing extra_body settings
	userARequest := []byte(`{"model": "qwen3", "extra_body": {"temperature": 0.7, "max_tokens": 1000}}`)
	userAHeaders := map[string]string{"x-user-id": "user-a-id"}

	userAMutated, err := mutator.MutateWithHeaders(userARequest, userAHeaders)
	require.NoError(t, err)

	// User B's request with same base settings
	userBRequest := []byte(`{"model": "qwen3", "extra_body": {"temperature": 0.7, "max_tokens": 1000}}`)
	userBHeaders := map[string]string{"x-user-id": "user-b-id"}

	userBMutated, err := mutator.MutateWithHeaders(userBRequest, userBHeaders)
	require.NoError(t, err)

	// Both should have preserved their original settings
	var userAResult, userBResult map[string]interface{}
	require.NoError(t, json.Unmarshal(userAMutated, &userAResult))
	require.NoError(t, json.Unmarshal(userBMutated, &userBResult))

	userAExtraBody := userAResult["extra_body"].(map[string]interface{})
	userBExtraBody := userBResult["extra_body"].(map[string]interface{})

	require.Equal(t, 0.7, userAExtraBody["temperature"])
	require.Equal(t, float64(1000), userAExtraBody["max_tokens"])
	require.Equal(t, 0.7, userBExtraBody["temperature"])
	require.Equal(t, float64(1000), userBExtraBody["max_tokens"])

	// But different cache_salt values
	userACacheSalt := userAExtraBody["cache_salt"].(string)
	userBCacheSalt := userBExtraBody["cache_salt"].(string)

	require.NotEqual(t, userACacheSalt, userBCacheSalt, "Different users should have different cache_salt values")
	require.Len(t, userACacheSalt, 44)
	require.Len(t, userBCacheSalt, 44)
}
