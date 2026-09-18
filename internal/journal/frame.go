package journal

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
)

var frameMagic = [4]byte{'D', 'P', 'J', '1'}

const frameHeaderBytes = 12

type ReadResult struct {
	Events        []Event
	TruncatedTail bool
	ValidBytes    int64
}

func Read(reader io.Reader) (ReadResult, error) {
	if reader == nil {
		return ReadResult{}, fmt.Errorf("journal reader must not be nil")
	}
	var result ReadResult
	var expected uint64 = 1
	var runID string
	for {
		var header [frameHeaderBytes]byte
		n, err := io.ReadFull(reader, header[:])
		if errors.Is(err, io.EOF) && n == 0 {
			return result, nil
		}
		if errors.Is(err, io.ErrUnexpectedEOF) || (errors.Is(err, io.EOF) && n > 0) {
			result.TruncatedTail = true
			return result, nil
		}
		if err != nil {
			return ReadResult{}, err
		}
		if !bytes.Equal(header[:4], frameMagic[:]) {
			return ReadResult{}, fmt.Errorf("%w at byte %d: invalid magic", ErrCorruptRecord, result.ValidBytes)
		}
		length := binary.BigEndian.Uint32(header[4:8])
		if length == 0 || length > MaxRecordBytes {
			return ReadResult{}, fmt.Errorf("%w at byte %d: invalid length %d", ErrCorruptRecord, result.ValidBytes, length)
		}
		payload := make([]byte, int(length))
		n, err = io.ReadFull(reader, payload)
		if errors.Is(err, io.ErrUnexpectedEOF) || (errors.Is(err, io.EOF) && n < len(payload)) {
			result.TruncatedTail = true
			return result, nil
		}
		if err != nil {
			return ReadResult{}, err
		}
		if got, want := crc32.ChecksumIEEE(payload), binary.BigEndian.Uint32(header[8:12]); got != want {
			return ReadResult{}, fmt.Errorf("%w at byte %d: checksum mismatch", ErrCorruptRecord, result.ValidBytes)
		}
		event, err := decodeEvent(payload)
		if err != nil {
			return ReadResult{}, fmt.Errorf("record at byte %d: %w", result.ValidBytes, err)
		}
		if event.Sequence != expected {
			return ReadResult{}, fmt.Errorf("%w: got %d, want %d", ErrSequence, event.Sequence, expected)
		}
		if runID == "" {
			runID = string(event.RunID)
		} else if string(event.RunID) != runID {
			return ReadResult{}, fmt.Errorf("journal contains multiple run IDs")
		}
		result.Events = append(result.Events, event)
		result.ValidBytes += frameHeaderBytes + int64(length)
		expected++
	}
}

func decodeEvent(payload []byte) (Event, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var event Event
	if err := decoder.Decode(&event); err != nil {
		return Event{}, fmt.Errorf("%w: %v", ErrCorruptRecord, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Event{}, fmt.Errorf("%w: trailing JSON data", ErrCorruptRecord)
	}
	if err := event.validate(); err != nil {
		return Event{}, err
	}
	return event, nil
}

func encodeFrame(event Event) ([]byte, error) {
	if err := event.validate(); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return nil, err
	}
	if len(payload) > MaxRecordBytes {
		return nil, fmt.Errorf("journal record exceeds %d bytes", MaxRecordBytes)
	}
	frame := make([]byte, frameHeaderBytes+len(payload))
	copy(frame[:4], frameMagic[:])
	binary.BigEndian.PutUint32(frame[4:8], uint32(len(payload)))
	binary.BigEndian.PutUint32(frame[8:12], crc32.ChecksumIEEE(payload))
	copy(frame[frameHeaderBytes:], payload)
	return frame, nil
}
