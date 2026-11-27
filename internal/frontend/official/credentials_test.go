package official

import (
	"strings"
	"testing"

	"github.com/aalsanie/distroplane/internal/domain"
)

var installedDefaults = map[string]bool{"npm": true, "sdkman": true, "homebrew": true, "winget": true}

type sourceUsage struct {
	provider    string
	target      string
	ref         string
	environment string
}

func planForSources(t *testing.T, inputs ...sourceUsage) domain.Plan {
	t.Helper()
	digest, _ := domain.NewSHA256Digest(strings.Repeat("a", 64))
	artifact, err := domain.NewArtifact("app", "app.bin", digest, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	releaseID, _ := domain.NewReleaseID("release")
	release, err := domain.NewRelease(releaseID, []domain.Artifact{artifact})
	if err != nil {
		t.Fatal(err)
	}
	config, err := domain.NewJSONValue([]byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	type entry struct {
		provider     string
		requirements []domain.Requirement
	}
	targets := map[string]*entry{}
	for _, input := range inputs {
		if targets[input.target] == nil {
			targets[input.target] = &entry{provider: input.provider}
		}
		if targets[input.target].provider != input.provider {
			t.Fatal("invalid fixture provider")
		}
		raw, err := domain.NewRequirement("credential", input.ref, []byte(`{"environment":"`+input.environment+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		targets[input.target].requirements = append(targets[input.target].requirements, raw)
	}
	resolvedTargets := make([]domain.Target, 0, len(targets))
	for id, entry := range targets {
		name, _ := domain.NewProviderName(entry.provider)
		version, _ := domain.NewProviderVersion("1.0.0")
		provider, err := domain.NewProviderRef(name, version)
		if err != nil {
			t.Fatal(err)
		}
		targetID, err := domain.NewTargetID(id)
		if err != nil {
			t.Fatal(err)
		}
		target, err := domain.NewTargetWithRequirements(targetID, provider, config, entry.requirements)
		if err != nil {
			t.Fatal(err)
		}
		resolvedTargets = append(resolvedTargets, target)
	}
	id, _ := domain.NewPlanID("credential-plan")
	plan, err := domain.NewPlan(id, "1", "1", release, resolvedTargets, nil)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestSelectSourcesPurposeSpecificDefaults(t *testing.T) {
	cases := []struct{ provider, environment, expected string }{
		{"npm", "DISTROPLANE_NPM_TOKEN", "NPM_TOKEN"},
		{"npm", "DISTROPLANE_NPM_ID_TOKEN", ""},
		{"sdkman", "DISTROPLANE_SDKMAN_VENDOR_EMAIL", "SDKMAN_VENDOR_EMAIL"},
		{"sdkman", "DISTROPLANE_SDKMAN_VENDOR_PASSWORD", "SDKMAN_VENDOR_PASSWORD"},
		{"sdkman", "DISTROPLANE_SDKMAN_CONSUMER_KEY", "SDKMAN_CONSUMER_KEY"},
		{"sdkman", "DISTROPLANE_SDKMAN_CONSUMER_TOKEN", "SDKMAN_CONSUMER_TOKEN"},
		{"homebrew", "DISTROPLANE_HOMEBREW_TOKEN", "HOMEBREW_GITHUB_TOKEN"},
		{"winget", "DISTROPLANE_WINGET_TOKEN", "WINGET_GITHUB_TOKEN"},
		{"other", "DISTROPLANE_NPM_TOKEN", ""},
		{"homebrew", "GITHUB_TOKEN", ""},
	}
	for _, test := range cases {
		t.Run(test.provider+"-"+test.environment, func(t *testing.T) {
			plan := planForSources(t, sourceUsage{test.provider, "only", "release", test.environment})
			sources, err := SelectSources(plan, nil, nil, installedDefaults)
			if err != nil {
				t.Fatal(err)
			}
			if got := DefaultSource(test.provider, test.environment); got != test.expected {
				t.Fatalf("candidate=%q want=%q", got, test.expected)
			}
			if got := sources["release"]; got != test.expected {
				t.Fatalf("got=%q want=%q", got, test.expected)
			}
		})
	}
}

