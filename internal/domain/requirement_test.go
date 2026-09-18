package domain

import "testing"

func TestRequirementValidationAndCopies(t *testing.T) {
	metadata := []byte(`{"environment":"TOKEN"}`)
	requirement, err := NewRequirement("credential", "release", metadata)
	if err != nil {
		t.Fatal(err)
	}
	metadata[0] = '['
	if requirement.Kind() != "credential" || requirement.Name() != "release" || string(requirement.Metadata()) != `{"environment":"TOKEN"}` || !requirement.Valid() {
		t.Fatalf("requirement=%+v", requirement)
	}
	copyMetadata := requirement.Metadata()
	copyMetadata[0] = '['
	if string(requirement.Metadata()) != `{"environment":"TOKEN"}` {
		t.Fatal("metadata was not copied")
	}
	for _, tc := range []struct {
		kind, name string
		metadata   []byte
	}{
		{"", "release", nil},
		{"credential", "", nil},
		{"credential", "release", []byte(`{`)},
	} {
		if _, err := NewRequirement(tc.kind, tc.name, tc.metadata); err == nil {
			t.Fatalf("accepted %+v", tc)
		}
	}
}

func TestTargetRequirementsAreDefensivelyCopied(t *testing.T) {
	id, _ := NewTargetID("target")
	name, _ := NewProviderName("provider")
	version, _ := NewProviderVersion("1")
	provider, _ := NewProviderRef(name, version)
	configuration, _ := NewJSONValue([]byte(`{}`))
	requirement, _ := NewRequirement("credential", "release", []byte(`{"environment":"TOKEN"}`))
	target, err := NewTargetWithRequirements(id, provider, configuration, []Requirement{requirement})
	if err != nil {
		t.Fatal(err)
	}
	requirements := target.Requirements()
	requirements[0].metadata[0] = '['
	if string(target.Requirements()[0].Metadata()) != `{"environment":"TOKEN"}` {
		t.Fatal("requirements were not copied")
	}
	if _, err := NewTargetWithRequirements(id, provider, configuration, []Requirement{{}}); err == nil {
		t.Fatal("invalid requirement accepted")
	}
	if _, err := NewTargetWithRequirements(id, provider, configuration, []Requirement{requirement, requirement}); err == nil {
		t.Fatal("duplicate requirement accepted")
	}
}
