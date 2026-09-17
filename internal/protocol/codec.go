package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"
)

type Codec struct {
	MaxMessageBytes int64
}

func NewCodec(maxMessageBytes int64) Codec {
	if maxMessageBytes <= 0 {
		maxMessageBytes = DefaultMaxMessageBytes
	}
	return Codec{MaxMessageBytes: maxMessageBytes}
}

func (c Codec) DecodeRequest(reader io.Reader) (Request, error) {
	data, err := c.read(reader)
	if err != nil {
		return Request{}, err
	}
	var request Request
	if err := decodeExact(data, &request); err != nil {
		return Request{}, fmt.Errorf("decode request: %w", err)
	}
	if err := request.Validate(); err != nil {
		return Request{}, err
	}
	return request, nil
}

func (c Codec) DecodeResponse(reader io.Reader) (Response, error) {
	data, err := c.read(reader)
	if err != nil {
		return Response{}, err
	}
	var response Response
	if err := decodeExact(data, &response); err != nil {
		return Response{}, fmt.Errorf("decode response: %w", err)
	}
	if err := response.Validate(); err != nil {
		return Response{}, err
	}
	return response, nil
}

func (c Codec) EncodeRequest(writer io.Writer, request Request) error {
	if err := request.Validate(); err != nil {
		return err
	}
	return c.write(writer, request)
}

func (c Codec) EncodeResponse(writer io.Writer, response Response) error {
	if err := response.Validate(); err != nil {
		return err
	}
	return c.write(writer, response)
}

func DecodePayload[T any](payload json.RawMessage, destination *T) error {
	if destination == nil {
		return fmt.Errorf("payload destination must not be nil")
	}
	if err := decodeExact(payload, destination); err != nil {
		return err
	}
	if validatable, ok := any(destination).(interface{ Validate() error }); ok {
		return validatable.Validate()
	}
	return nil
}

func EncodePayload[T any](value T) (json.RawMessage, error) {
	if validatable, ok := any(value).(interface{ Validate() error }); ok {
		if err := validatable.Validate(); err != nil {
			return nil, err
		}
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func (c Codec) read(reader io.Reader) ([]byte, error) {
	if reader == nil {
		return nil, fmt.Errorf("reader must not be nil")
	}
	limited := io.LimitReader(reader, c.MaxMessageBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > c.MaxMessageBytes {
		return nil, fmt.Errorf("protocol message exceeds %d bytes", c.MaxMessageBytes)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, fmt.Errorf("protocol message is empty")
	}
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("protocol message must be valid UTF-8")
	}
	return data, nil
}

func (c Codec) write(writer io.Writer, value any) error {
	if writer == nil {
		return fmt.Errorf("writer must not be nil")
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if int64(len(data)) > c.MaxMessageBytes {
		return fmt.Errorf("protocol message exceeds %d bytes", c.MaxMessageBytes)
	}
	_, err = writer.Write(data)
	return err
}

func decodeExact(data []byte, destination any) error {
	if len(data) == 0 || !utf8.Valid(data) {
		return fmt.Errorf("invalid JSON input")
	}
	if err := rejectDuplicateKeys(data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("multiple JSON values are not allowed")
		}
		return fmt.Errorf("trailing data: %w", err)
	}
	return nil
}

func rejectDuplicateKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := scanJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}

func scanJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		keys := map[string]struct{}{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("object key must be a string")
			}
			if _, exists := keys[key]; exists {
				return fmt.Errorf("duplicate JSON object key %q", key)
			}
			keys[key] = struct{}{}
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil {
			return err
		}
		if end != json.Delim('}') {
			return fmt.Errorf("invalid JSON object terminator")
		}
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil {
			return err
		}
		if end != json.Delim(']') {
			return fmt.Errorf("invalid JSON array terminator")
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
	return nil
}
