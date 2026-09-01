package httpapi

import (
	"encoding/json"
	"strings"
)

// List hygiene shared by handlers whose domain has not been extracted yet.
// The agents package carries its own copy rather than importing this one: a
// domain must not depend on the transport layer, and two unrelated domains
// must not depend on each other for twenty lines of string trimming.

// decodeStringList turns a jsonb column into a slice that is never nil, so the
// response carries [] rather than null and the client needs no special case.
func decodeStringList(raw []byte) []string {
	values := []string{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &values)
	}
	if values == nil {
		values = []string{}
	}
	return values
}

// cleanStringList trims, drops blanks and truncates, so a list arriving from a
// form cannot store empty entries or grow without bound.
func cleanStringList(values []string, limit, maxRunes int) []string {
	cleaned := []string{}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if len([]rune(trimmed)) > maxRunes {
			trimmed = string([]rune(trimmed)[:maxRunes])
		}
		cleaned = append(cleaned, trimmed)
		if len(cleaned) >= limit {
			break
		}
	}
	return cleaned
}
