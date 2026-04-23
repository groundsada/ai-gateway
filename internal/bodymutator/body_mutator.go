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

// NewBodyMutator creates a new BodyMutator with the given mutations.
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

// Mutate mutates the request body based on the body mutations.
// Headers can be provided to resolve dynamic values from request context.
func (b *BodyMutator) Mutate(requestBody []byte) ([]byte, error) {
	return b.MutateWithHeaders(requestBody, nil)
}

// MutateWithHeaders mutates the request body based on the body mutations,
// using headers to resolve dynamic values.
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
		for i := range b.bodyMutations.Set {
			field := &b.bodyMutations.Set[i]
			if field.Path == "" {
				continue
			}

			// Resolve the value - either static or from header
			value, valueErr := b.resolveFieldValue(field, headers)
			if valueErr != nil {
				return nil, fmt.Errorf("failed to resolve value for field %s: %w", field.Path, valueErr)
			}
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

// resolveFieldValue resolves the value for a field, either from static value or from headers.
func (b *BodyMutator) resolveFieldValue(field *filterapi.HTTPBodyField, headers map[string]string) (string, error) {
	// Handle dynamic value from header
	if field.ValueFrom != nil {
		if headers == nil {
			return "", nil // Skip if no headers provided
		}
		headerValue := headers[field.ValueFrom.HeaderName]
		if headerValue == "" {
			return "", nil // Skip if header not present
		}

		var resultValue interface{} = headerValue

		if field.ValueFrom.Hash == "sha256" {
			hash := sha256.Sum256([]byte(headerValue))
			switch field.ValueFrom.Encoding {
			case "base64":
				resultValue = base64.StdEncoding.EncodeToString(hash[:])
			default:
				// Return as JSON array of bytes
				jsonBytes, _ := json.Marshal(hash[:])
				resultValue = string(jsonBytes)
			}
		}

		// Convert to string for sjson
		switch v := resultValue.(type) {
		case string:
			// If it's a JSON value (quoted string), keep as-is
			// Otherwise, wrap in quotes for JSON string
			if isJSONValue(v) {
				return v, nil
			}
			return fmt.Sprintf("%q", v), nil
		default:
			// For other types, marshal to JSON
			jsonBytes, err := json.Marshal(v)
			if err != nil {
				return "", err
			}
			return string(jsonBytes), nil
		}
	}

	// Handle static value
	return field.Value, nil
}

// mergeAtPath merges a JSON value at the given path.
// If the path points to an existing object, it adds/updates keys from the value.
// If the path doesn't exist, it creates it.
func (b *BodyMutator) mergeAtPath(body []byte, path string, value string) ([]byte, error) {
	// Parse the body as JSON
	var jsonBody map[string]interface{}
	if err := json.Unmarshal(body, &jsonBody); err != nil {
		return nil, fmt.Errorf("failed to parse body as JSON: %w", err)
	}

	// Parse the value as JSON
	var newValue interface{}
	if err := json.Unmarshal([]byte(value), &newValue); err != nil {
		return nil, fmt.Errorf("failed to parse value as JSON: %w", err)
	}

	// Navigate to the parent of the target path
	parts := strings.Split(path, ".")
	current := jsonBody

	for i := 0; i < len(parts)-1; i++ {
		part := parts[i]
		if _, exists := current[part]; !exists {
			// Path doesn't exist, just set the value
			current[part] = make(map[string]interface{})
		} else if _, ok := current[part].(map[string]interface{}); !ok {
			// Path exists but is not an object, can't merge
			// Fall back to setting the value
			current[part] = make(map[string]interface{})
		}
		current = current[part].(map[string]interface{})
	}

	// Get the final key
	finalKey := parts[len(parts)-1]

	// Get existing value at final key
	existing := current[finalKey]

	if existing != nil {
		// Merge the new value into existing
		existingMap, existingIsMap := existing.(map[string]interface{})
		newMap, newIsMap := newValue.(map[string]interface{})

		if existingIsMap && newIsMap {
			// Deep merge
			current[finalKey] = deepMerge(existingMap, newMap)
		} else {
			// Can't merge non-objects, replace
			current[finalKey] = newValue
		}
	} else {
		// No existing value, just set
		current[finalKey] = newValue
	}

	// Marshal back to JSON
	result, err := json.Marshal(jsonBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal merged body: %w", err)
	}

	return result, nil
}

// deepMerge recursively merges src into dst.
func deepMerge(dst, src map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})

	// Copy dst
	for k, v := range dst {
		result[k] = v
	}

	// Merge src
	for k, v := range src {
		if dstVal, exists := dst[k]; exists {
			dstMap, ok1 := dstVal.(map[string]interface{})
			srcMap, ok2 := v.(map[string]interface{})
			if ok1 && ok2 {
				// Both are maps, recursively merge
				result[k] = deepMerge(dstMap, srcMap)
			} else {
				// Not both maps, src wins
				result[k] = v
			}
		} else {
			result[k] = v
		}
	}

	return result
}
