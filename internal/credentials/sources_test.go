package credentials

import (
	"context"
	"errors"
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
			if got := sources["release"]; got != test.expected {
				t.Fatalf("got=%q want=%q", got, test.expected)
			}
		})
	}
}

func TestSelectSourcesPrecedenceAndLazyResolution(t *testing.T) {
	plan := planForSources(t, sourceUsage{"npm", "one", "npm-publish", "DISTROPLANE_NPM_TOKEN"})
	configured := map[string]string{"npm-publish": "CONFIG_NPM_TOKEN"}
	explicit := map[domain.CredentialRef]string{"npm-publish": "CLI_NPM_TOKEN"}
	t.Setenv("CONFIG_NPM_TOKEN", "config-canary")
	t.Setenv("CLI_NPM_TOKEN", "cli-canary")
	sources, err := SelectSources(plan, configured, explicit, installedDefaults)
	if err != nil || sources["npm-publish"] != "CLI_NPM_TOKEN" {
		t.Fatalf("sources=%v err=%v", sources, err)
	}
	resolver, err := NewEnvironmentResolver(sources)
	if err != nil {
		t.Fatal(err)
	}
	material, err := resolver.Resolve(context.Background(), "npm-publish")
	if err != nil {
		t.Fatal(err)
	}
	value, err := material.Bytes()
	material.Release()
	if err != nil || string(value) != "cli-canary" {
		t.Fatal("explicit source not respected")
	}
	sources, err = SelectSources(plan, configured, nil, installedDefaults)
	if err != nil || sources["npm-publish"] != "CONFIG_NPM_TOKEN" {
		t.Fatalf("sources=%v err=%v", sources, err)
	}
	sources, err = SelectSources(plan, nil, nil, installedDefaults)
	if err != nil || sources["npm-publish"] != "NPM_TOKEN" {
		t.Fatalf("sources=%v err=%v", sources, err)
	}
}

func TestSelectSourcesMultiAccountIsolation(t *testing.T) {
	plan := planForSources(t,
		sourceUsage{"npm", "primary", "npm-primary", "DISTROPLANE_NPM_TOKEN"},
		sourceUsage{"npm", "secondary", "npm-secondary", "DISTROPLANE_NPM_TOKEN"},
	)
	t.Setenv("NPM_TOKEN", "ambient-canary")
	sources, err := SelectSources(plan, nil, nil, installedDefaults)
	if err != nil || len(sources) != 0 {
		t.Fatalf("unapproved shared fallback: %v err=%v", sources, err)
	}
	sources, err = SelectSources(plan, map[string]string{"npm-primary": "NPM_A", "npm-secondary": "NPM_B"}, nil, installedDefaults)
	if err != nil || len(sources) != 2 || sources["npm-primary"] != "NPM_A" || sources["npm-secondary"] != "NPM_B" {
		t.Fatalf("isolated sources=%v err=%v", sources, err)
	}
	_, err = SelectSources(plan, map[string]string{"npm-primary": "NPM_TOKEN", "npm-secondary": "NPM_TOKEN"}, nil, installedDefaults)
	if !errors.Is(err, ErrAmbiguousSource) {
		t.Fatalf("shared source accepted: %v", err)
	}
}

func TestSelectSourcesCrossProviderCollisionsAndExplicitOverrides(t *testing.T) {
	plan := planForSources(t,
		sourceUsage{"homebrew", "brew", "brew-auth", "DISTROPLANE_HOMEBREW_TOKEN"},
		sourceUsage{"winget", "win", "winget-auth", "DISTROPLANE_WINGET_TOKEN"},
	)
	sources, err := SelectSources(plan, nil, nil, installedDefaults)
	if err != nil || sources["brew-auth"] == sources["winget-auth"] {
		t.Fatalf("provider defaults not isolated: %v err=%v", sources, err)
	}
	_, err = SelectSources(plan, nil, map[domain.CredentialRef]string{"brew-auth": "GITHUB_TOKEN", "winget-auth": "GITHUB_TOKEN"}, installedDefaults)
	if !errors.Is(err, ErrAmbiguousSource) {
		t.Fatalf("shared github token accepted: %v", err)
	}
	plan = planForSources(t,
		sourceUsage{"homebrew", "brew", "shared", "DISTROPLANE_HOMEBREW_TOKEN"},
		sourceUsage{"winget", "win", "shared", "DISTROPLANE_WINGET_TOKEN"},
	)
	_, err = SelectSources(plan, nil, nil, installedDefaults)
	if !errors.Is(err, ErrSourceConflict) {
		t.Fatalf("shared cross-provider reference accepted: %v", err)
	}
}

func TestSelectSourcesValidatesMappings(t *testing.T) {
	plan := planForSources(t, sourceUsage{"npm", "only", "release", "DISTROPLANE_NPM_TOKEN"})
	tests := []struct {
		name     string
		config   map[string]string
		explicit map[domain.CredentialRef]string
	}{
		{"unknown configured", map[string]string{"typo": "SOME_ENV"}, nil},
		{"unknown explicit", nil, map[domain.CredentialRef]string{"typo": "SOME_ENV"}},
		{"invalid configured", map[string]string{"release": "BAD-ENV"}, nil},
		{"invalid explicit", nil, map[domain.CredentialRef]string{"release": "BAD=ENV"}},
		{"invalid ref", map[string]string{"": "VALID_ENV"}, nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := SelectSources(plan, test.config, test.explicit, installedDefaults); err == nil {
				t.Fatal("invalid mapping was accepted")
			}
		})
	}
}

func TestSelectSourcesBlocksUnknownWithoutInheritingAmbientEnvironment(t *testing.T) {
	plan := planForSources(t, sourceUsage{"custom", "only", "release", "PROVIDER_SECRET"})
	t.Setenv("PROVIDER_SECRET", "ambient-secret")
	sources, err := SelectSources(plan, nil, nil, installedDefaults)
	if err != nil || len(sources) != 0 {
		t.Fatalf("ambient credential inherited: %v err=%v", sources, err)
	}
}
