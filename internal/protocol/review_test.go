package protocol

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestReviewFieldsBoundsAndUnsafeText(t *testing.T) {
	valid := ReviewFields{{Label: strings.Repeat("界", 80), Value: strings.Repeat("é", 512)}}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (ReviewFields(nil)).Validate(); err != nil {
		t.Fatal(err)
	}
	fields := make(ReviewFields, 16)
	for i := range fields {
		fields[i] = ReviewField{Label: fmt.Sprintf("Field %d", i), Value: "example"}
	}
	if err := fields.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := append(fields, ReviewField{Label: "Extra", Value: "x"}).Validate(); err == nil {
		t.Fatal("17 entries accepted")
	}
	cases := []ReviewFields{
		{{Label: "", Value: "x"}},
		{{Label: "x", Value: ""}},
		{{Label: strings.Repeat("界", 81), Value: "x"}},
		{{Label: "x", Value: strings.Repeat("é", 513)}},
		{{Label: "x", Value: " leading"}},
		{{Label: "x ", Value: "trailing"}},
		{{Label: "x", Value: string([]byte{0xff})}},
		{{Label: "x", Value: "x\x1b[2J"}},
		{{Label: "x", Value: "x\nforged approval"}},
		{{Label: "x", Value: "x\u009b"}},
		{{Label: "x", Value: "x\u202e"}},
		{{Label: "x", Value: "x\u2066"}},
		{{Label: "x", Value: "x\u2028"}},
		{{Label: "x", Value: "x\u2029"}},
		{{Label: "x", Value: "x\ufffd"}},
		{{Label: "x", Value: "one"}, {Label: "x", Value: "two"}},
		{{Label: "x", Value: strings.Repeat("&", 512)}, {Label: "y", Value: strings.Repeat("&", 512)}, {Label: "z", Value: strings.Repeat("&", 512)}},
	}
	for i, fields := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			if err := fields.Validate(); err == nil {
				t.Fatal("unsafe review fields accepted")
			}
			if _, err := EncodePayload(PlanResponse{ReviewFields: fields}); err == nil {
				t.Fatal("unsafe review fields encoded")
			}
		})
	}
}

func TestReviewFieldsStrictWireDecodingAndOrder(t *testing.T) {
	raw := json.RawMessage(`{"operations":[],"reviewFields":[{"label":"Second","value":"例"},{"label":"First","value":"release"}],"future":true}`)
	var response PlanResponse
	if err := DecodePayload(raw, &response); err != nil {
		t.Fatal(err)
	}
	want := ReviewFields{{Label: "Second", Value: "例"}, {Label: "First", Value: "release"}}
	if !reflect.DeepEqual(response.ReviewFields, want) {
		t.Fatalf("order changed: %v", response.ReviewFields)
	}
	encoded, err := EncodePayload(response)
	if err != nil {
		t.Fatal(err)
	}
	var decoded PlanResponse
	if err := DecodePayload(encoded, &decoded); err != nil || !reflect.DeepEqual(decoded.ReviewFields, want) {
		t.Fatalf("round trip=%v err=%v", decoded.ReviewFields, err)
	}
	for _, value := range []string{
		`null`, `{}`, `["text"]`, `[null]`, `[{"label":"x"}]`,
		`[{"label":"x","value":"y","extra":true}]`,
		`[{"Label":"x","value":"y"}]`, `[{"label":2,"value":"y"}]`,
		`[{"label":"x","value":false}]`, `[{"label":"x","value":null}]`,
		`[{"label":"x","value":"y","value":"z"}]`,
		`[{"label":"x","value":"y"},{"label":"x","value":"z"}]`,
		`[{"label":"x","value":"\ud800"}]`,
		`[{"label":"x","value":"\u001b[2J"}]`,
		`[{"label":"x","value":"\u202e"}]`,
		"[" + strings.Repeat(`{"label":"x","value":"y"},`, 16) + `{"label":"last","value":"y"}]`,
	} {
		t.Run(value, func(t *testing.T) {
			var fields ReviewFields
			if err := fields.UnmarshalJSON([]byte(value)); err == nil {
				t.Fatal("invalid review fields accepted")
			}
		})
	}
	if err := (&ReviewFields{}).UnmarshalJSON([]byte{'[', 0xff, ']'}); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
	for _, value := range []string{`{"operations":[]}`, `{"operations":[],"reviewFields":[]}`} {
		var response PlanResponse
		if err := DecodePayload(json.RawMessage(value), &response); err != nil || len(response.ReviewFields) != 0 {
			t.Fatalf("summary-less response rejected: %v", err)
		}
	}
}
