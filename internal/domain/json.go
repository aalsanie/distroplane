package domain

import (
	"encoding/json"
	"fmt"
)

type JSONValue struct {
	raw []byte
}

func NewJSONValue(raw []byte) (JSONValue, error) {
	if len(raw) == 0 || !json.Valid(raw) {
		return JSONValue{}, fmt.Errorf("invalid JSON value")
	}
	return JSONValue{raw: append([]byte(nil), raw...)}, nil
}

func (v JSONValue) Bytes() []byte {
	return append([]byte(nil), v.raw...)
}

func (v JSONValue) Valid() bool {
	return len(v.raw) != 0 && json.Valid(v.raw)
}

func (v JSONValue) MarshalJSON() ([]byte, error) {
	if !v.Valid() {
		return nil, fmt.Errorf("invalid JSON value")
	}
	return v.Bytes(), nil
}

func (v *JSONValue) UnmarshalJSON(data []byte) error {
	parsed, err := NewJSONValue(data)
	if err != nil {
		return err
	}
	*v = parsed
	return nil
}
