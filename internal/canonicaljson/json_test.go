package canonicaljson

import (
	"bytes"
	"testing"
)

func TestNormalize(t *testing.T) {
	got, err := Normalize([]byte(` { "z" : [3, {"b":2,"a":1}], "a": true, "n": null } `))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"a":true,"n":null,"z":[3,{"a":1,"b":2}]}`
	if string(got) != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestNormalizeRejectsInvalidInputs(t *testing.T) {
	cases := [][]byte{
		nil,
		[]byte(" \n\t"),
		{0xff},
		[]byte(`{"a":1,"a":2}`),
		[]byte(`{"a":1} {"b":2}`),
		[]byte(`{"a":1} trailing`),
		[]byte(`{"a":`),
		[]byte(`{"a"`),
		[]byte(`{"a":[1,`),
	}
	for _, input := range cases {
		if _, err := Normalize(input); err == nil {
			t.Fatalf("expected error for %q", input)
		}
	}
}

func TestNormalizeScalarsAndArrays(t *testing.T) {
	cases := map[string]string{
		`1.0`:                  `1.0`,
		`"x"`:                  `"x"`,
		`false`:                `false`,
		`[{"b":2,"a":1},null]`: `[{"a":1,"b":2},null]`,
	}
	for input, want := range cases {
		got, err := Normalize([]byte(input))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("%s: got %s want %s", input, got, want)
		}
	}
}

func FuzzNormalize(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(`{"b":2,"a":1}`),
		[]byte(`[1,true,null,"x"]`),
		[]byte(`{"nested":{"z":0,"a":[]}}`),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		first, err := Normalize(raw)
		if err != nil {
			return
		}
		second, err := Normalize(first)
		if err != nil {
			t.Fatalf("normalized value rejected: %v", err)
		}
		if !bytes.Equal(first, second) {
			t.Fatalf("normalization is not idempotent")
		}
	})
}
