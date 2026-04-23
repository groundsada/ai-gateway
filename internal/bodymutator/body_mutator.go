// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package bodymutator

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/json"
)

// BodyMutator handles request body mutations with support for merging and dynamic values.
type BodyMutator struct {
	// originalBody is the original request body for retry scenarios
	originalBody []byte

	// bodyMutations is the body mutations to apply
	bodyMutations *filterapi.HTTPBodyMutation
}

func NewBodyMutator(bodyMutations *filterapi.HTTPBodyMutation, originalBody []byte) *BodyMutator {
	return &BodyMutator{
		originalBody:  originalBody,
		bodyMutations: bodyMutations,
	}
}

// isJSONValue checks if a string represents a JSON value (not a plain string)
func isJSONValue(value string) bool {
	value = strings.TrimSpace(value)

	// Check for quoted strings (JSON strings)
	if strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"") {
		return true
	}

	// Check for numbers (integers or floats)
	if value == "0" || value == "true" || value == "false" || value == "null" {
		return true
	}

	// Check for positive/negative numbers
	if len(value) > 0 {
		first := value[0]
		if (first >= '0' && first <= '9') || first == '-' || first == '+' {
			// Simple number check - contains only digits, dots, +, -, e, E
			isNumber := true
			for _, r := range value {
				if (r < '0' || r > '9') && r != '.' && r != '-' && r != '+' && r != 'e' && r != 'E' {
					isNumber = false
					break
				}
			}
			if isNumber {
				return true
			}
		}
	}

	// Check for objects or arrays
	if strings.HasPrefix(value, "{") && strings.HasSuffix(value, "}") {
		return true
	}
	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		return true
	}

	// Default to plain string
	return false
}

// mergeAtPath merges a JSON value into an existing object at the given path.
// This preserves existing fields while adding/updating the specified value.
func (b *BodyMutator) mergeAtPath(body []byte, path string, value string) ([]byte, error) {
	// Get the existing value at the path using gjson
	existingResult := gjson.Get(string(body), path)

	var mergedValue []byte
	var err error

	if !existingResult.Exists() || existingResult.Raw == "null" {
		// No existing value, just set it
		if isJSONValue(value) {
			mergedValue = []byte(value)
		} else {
			mergedValue = []byte(fmt.Sprintf(`"%s"`, value))
		}
	} else {
		// Existing value found - parse and merge
		var existing interface{}
		if unmarshalErr := json.Unmarshal([]byte(existingResult.Raw), &existing); unmarshalErr != nil {
			// Can't parse existing value as JSON, just replace
			if isJSONValue(value) {
				mergedValue = []byte(value)
			} else {
				mergedValue = []byte(fmt.Sprintf(`"%s"`, value))
			}
		} else {
			// Parse the new value
			var newValue interface{}
			newUnmarshalErr := json.Unmarshal([]byte(value), &newValue)
			if newUnmarshalErr != nil {
				// Can't parse new value as JSON, wrap it as string
				newValue = value
			}

			// Merge: existing map + new value
			if existingMap, ok := existing.(map[string]interface{}); ok {
				if newMap, ok := newValue.(map[string]interface{}); ok {
					// Both are maps, merge recursively
					for k, v := range newMap {
						existingMap[k] = v
					}
					mergedValue, err = json.Marshal(existingMap)
					if err != nil {
						return nil, fmt.Errorf("failed to marshal merged value: %w", err)
					}
				} else {
					// New value is not a map, just set it directly
					mergedValue, err = json.Marshal(newValue)
					if err != nil {
						return nil, fmt.Errorf("failed to marshal new value: %w", err)
					}
				}
			} else {
				// Existing is not a map, replace with new value
				mergedValue, err = json.Marshal(newValue)
				if err != nil {
					return nil, fmt.Errorf("failed to marshal new value: %w", err)
				}
			}
		}
	}

	// Set the merged value at the path using SetRawBytes for JSON
	return sjson.SetRawBytesOptions(body, path, mergedValue, &sjson.Options{ReplaceInPlace: true})
}

// resolveValue resolves a field value, potentially from headers
func (b *BodyMutator) resolveValue(field *filterapi.HTTPBodyField, headers map[string]string) string {
	// If valueFrom is specified, extract from headers
	if field.ValueFrom != nil && field.ValueFrom.HeaderName != "" {
		value := getHeader(headers, field.ValueFrom.HeaderName)
		if value == "" {
			return ""
		}

		// Apply hash if specified
		if field.ValueFrom.Hash == "sha256" {
			hash := sha256.Sum256([]byte(value))
			value = string(hash[:])
		}

		// Apply encoding if specified
		if field.ValueFrom.Encoding == "base64" {
			value = base64.StdEncoding.EncodeToString([]byte(value))
		}

		return value
	}

	// Otherwise use static value
	return field.Value
}

// MutateWithHeaders mutates the request body based on the body mutations, using headers for dynamic values.
func (b *BodyMutator) MutateWithHeaders(requestBody []byte, headers map[string]string) ([]byte, error) {
	if b.bodyMutations == nil {
		return requestBody, nil
	}

	mutatedBody := requestBody
	var err error

	// Apply removals first
	if len(b.bodyMutations.Remove) > 0 {
		for _, fieldName := range b.bodyMutations.Remove {
			if fieldName != "" {
				mutatedBody, err = sjson.DeleteBytes(mutatedBody, fieldName)
				if err != nil {
					return nil, fmt.Errorf("failed to remove field %s: %w", fieldName, err)
				}
			}
		}
	}

	// Apply sets
	if len(b.bodyMutations.Set) > 0 {
		for _, field := range b.bodyMutations.Set {
			if field.Path == "" {
				continue
			}

			// Resolve value (static or from headers)
			value := b.resolveValue(&field, headers)
			if value == "" {
				continue // Skip empty values
			}

			// Apply mutation based on merge flag
			switch {
			case field.Merge != nil && *field.Merge:
				mutatedBody, err = b.mergeAtPath(mutatedBody, field.Path, value)
				if err != nil {
					return nil, fmt.Errorf("failed to merge field %s: %w", field.Path, err)
				}
			case isJSONValue(value):
				// Use SetRawBytes for JSON values (quoted strings, numbers, booleans, objects, arrays)
				mutatedBody, err = sjson.SetRawBytesOptions(mutatedBody, field.Path, []byte(value), &sjson.Options{ReplaceInPlace: true})
				if err != nil {
					return nil, fmt.Errorf("failed to set field %s: %w", field.Path, err)
				}
			default:
				// Use SetBytes for plain string values
				mutatedBody, err = sjson.SetBytesOptions(mutatedBody, field.Path, value, &sjson.Options{ReplaceInPlace: true})
				if err != nil {
					return nil, fmt.Errorf("failed to set field %s: %w", field.Path, err)
				}
			}
		}
	}

	return mutatedBody, nil
}

// Mutate mutates the request body based on the body mutations.
func (b *BodyMutator) Mutate(requestBody []byte) ([]byte, error) {
	return b.MutateWithHeaders(requestBody, nil)
}

// getHeader returns the value for key, case-insensitive.
func getHeader(headers map[string]string, key string) string {
	keyLower := strings.ToLower(key)
	for k, v := range headers {
		if strings.EqualFold(k, keyLower) {
			return v
		}
	}
	return ""
}
