package providerhost

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"

	"github.com/aalsanie/distroplane/internal/protocol"
)

type redactor struct {
	values []string
}

func newRedactor(values [][]byte) redactor {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if len(value) == 0 {
			continue
		}
		secret := string(value)
		if _, exists := seen[secret]; exists {
			continue
		}
		seen[secret] = struct{}{}
		result = append(result, secret)
	}
	sort.Slice(result, func(i, j int) bool { return len(result[i]) > len(result[j]) })
	return redactor{values: result}
}

func (r redactor) text(value string) string {
	for _, secret := range r.values {
		value = strings.ReplaceAll(value, secret, "[REDACTED]")
	}
	return value
}

func (r redactor) json(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || len(r.values) == 0 {
		return append(json.RawMessage(nil), raw...)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return append(json.RawMessage(nil), raw...)
	}
	value = r.value(value)
	encoded, err := json.Marshal(value)
	if err != nil {
		return append(json.RawMessage(nil), raw...)
	}
	return encoded
}

func (r redactor) value(value any) any {
	switch typed := value.(type) {
	case string:
		return r.text(typed)
	case []any:
		for i := range typed {
			typed[i] = r.value(typed[i])
		}
		return typed
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, child := range typed {
			result[r.text(key)] = r.value(child)
		}
		return result
	default:
		return value
	}
}

func redactProviderError(value *protocol.ProviderError, r redactor) {
	if value == nil || len(r.values) == 0 {
		return
	}
	value.Message = r.text(value.Message)
	value.Details = r.json(value.Details)
}

func redactDestination(destination any, r redactor) {
	if len(r.values) == 0 {
		return
	}
	switch typed := destination.(type) {
	case *protocol.ApplyResponse:
		redactDistributionResult(&typed.Result, r)
	case *protocol.ReconcileResponse:
		redactDistributionResult(&typed.Result, r)
	}
}

func redactDistributionResult(result *protocol.DistributionResult, r redactor) {
	if result == nil {
		return
	}
	result.ProviderState = r.text(result.ProviderState)
	result.Evidence = r.json(result.Evidence)
}
