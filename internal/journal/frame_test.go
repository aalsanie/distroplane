package journal

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"strings"
	"testing"
)

func rawFrame(t testing.TB, value any) []byte {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	frame := make([]byte, frameHeaderBytes+len(payload))
	copy(frame[:4], frameMagic[:])
	binary.BigEndian.PutUint32(frame[4:8], uint32(len(payload)))
	binary.BigEndian.PutUint32(frame[8:12], crc32.ChecksumIEEE(payload))
	copy(frame[frameHeaderBytes:], payload)
	return frame
}

func TestReadEmptyAndValidJournal(t *testing.T) {
	result, err := Read(strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Events) != 0 || result.TruncatedTail || result.ValidBytes != 0 {
		t.Fatalf("result=%+v", result)
	}
	first := mustFrame(t, runStarted(1))
	second := mustFrame(t, attemptStarted(2, "op-a", "target-a", 1))
	result, err = Read(bytes.NewReader(append(first, second...)))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Events) != 2 || result.TruncatedTail || result.ValidBytes != int64(len(first)+len(second)) {
		t.Fatalf("result=%+v", result)
	}
}

func TestReadTruncatedTail(t *testing.T) {
	complete := mustFrame(t, runStarted(1))
	next := mustFrame(t, attemptStarted(2, "op-a", "target-a", 1))
	for _, cut := range []int{1, frameHeaderBytes - 1, frameHeaderBytes + 3, len(next) - 1} {
		data := append(append([]byte(nil), complete...), next[:cut]...)
		result, err := Read(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("cut=%d err=%v", cut, err)
		}
		if !result.TruncatedTail || len(result.Events) != 1 || result.ValidBytes != int64(len(complete)) {
			t.Fatalf("cut=%d result=%+v", cut, result)
		}
	}
}

func TestReadRejectsCorruption(t *testing.T) {
	first := mustFrame(t, runStarted(1))
	second := mustFrame(t, attemptStarted(2, "op-a", "target-a", 1))
	third := mustFrame(t, dispatched(3, "op-a", "target-a", 1))
	cases := map[string][]byte{}
	badMagic := append([]byte(nil), first...)
	badMagic[0] = 'X'
	cases["magic"] = badMagic
	badLength := append([]byte(nil), first...)
	binary.BigEndian.PutUint32(badLength[4:8], MaxRecordBytes+1)
	cases["length"] = badLength
	badChecksum := append(append([]byte(nil), first...), second...)
	badChecksum[len(first)+frameHeaderBytes] ^= 0xff
	badChecksum = append(badChecksum, third...)
	cases["middle checksum"] = badChecksum
	badJSON := rawFrame(t, map[string]any{"schemaVersion": 1, "sequence": 1, "runId": "run-1", "type": "RUN_STARTED", "observedAt": "not-time", "payload": map[string]any{}})
	cases["json"] = badJSON
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Read(bytes.NewReader(data))
			if err == nil {
				t.Fatal("corruption accepted")
			}
		})
	}
}

func TestReadRejectsSequenceAndRunMismatch(t *testing.T) {
	gap := append(mustFrame(t, runStarted(1)), mustFrame(t, attemptStarted(3, "op-a", "target-a", 1))...)
	if _, err := Read(bytes.NewReader(gap)); !errors.Is(err, ErrSequence) {
		t.Fatalf("gap err=%v", err)
	}
	dup := append(mustFrame(t, runStarted(1)), mustFrame(t, attemptStarted(1, "op-a", "target-a", 1))...)
	if _, err := Read(bytes.NewReader(dup)); !errors.Is(err, ErrSequence) {
		t.Fatalf("dup err=%v", err)
	}
	other := attemptStarted(2, "op-a", "target-a", 1)
	other.RunID = "run-2"
	mixed := append(mustFrame(t, runStarted(1)), mustFrame(t, other)...)
	if _, err := Read(bytes.NewReader(mixed)); err == nil {
		t.Fatal("mixed run IDs accepted")
	}
}

func TestReadCompatibilityPolicy(t *testing.T) {
	future := runStarted(1)
	future.SchemaVersion = SchemaVersion + 1
	if _, err := Read(bytes.NewReader(rawFrame(t, future))); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("future version err=%v", err)
	}
	unknown := runStarted(1)
	unknown.Type = "FUTURE_EVENT"
	if _, err := Read(bytes.NewReader(rawFrame(t, unknown))); !errors.Is(err, ErrUnknownEventType) {
		t.Fatalf("unknown type err=%v", err)
	}
	payload, _ := json.Marshal(runStarted(1))
	var object map[string]any
	_ = json.Unmarshal(payload, &object)
	object["futureField"] = true
	if _, err := Read(bytes.NewReader(rawFrame(t, object))); err == nil {
		t.Fatal("unknown same-version field accepted")
	}
}

func TestReadRejectsNilReader(t *testing.T) {
	if _, err := Read(nil); err == nil {
		t.Fatal("nil reader accepted")
	}
}

func FuzzRead(f *testing.F) {
	f.Add(mustFrame(f, runStarted(1)))
	valid := append(mustFrame(f, runStarted(1)), mustFrame(f, attemptStarted(2, "op-a", "target-a", 1))...)
	f.Add(valid)
	f.Add(valid[:len(valid)-1])
	f.Fuzz(func(t *testing.T, data []byte) { _, _ = Read(bytes.NewReader(data)) })
}
