package credentials

import (
	"errors"
	"fmt"
	"sort"

	"github.com/aalsanie/distroplane/internal/domain"
)

var (
	ErrAmbiguousSource = errors.New("credential source is ambiguous")
	ErrSourceConflict  = errors.New("credential reference is shared by incompatible destinations")
)

// SelectSources chooses environment variable names for credential requirements
// from a verified plan. Credential bytes are never read during this step.
func SelectSources(plan domain.Plan, configured map[string]string, explicit map[domain.CredentialRef]string, trustedProviders map[string]bool) (map[domain.CredentialRef]string, error) {
	type usage struct {
		provider    string
		environment string
	}
	uses := make(map[domain.CredentialRef]usage)
	providerTargets := make(map[string]map[domain.TargetID]struct{})
	for _, target := range plan.Targets() {
		provider := string(target.Provider().Name())
		for _, raw := range target.Requirements() {
			if raw.Kind() != "credential" {
				continue
			}
			requirement, err := ParseRequirement(raw)
			if err != nil {
				return nil, fmt.Errorf("target %q: %w", target.ID(), err)
			}
			if previous, exists := uses[requirement.Ref]; exists {
				if previous.provider != provider || previous.environment != requirement.Environment {
					return nil, fmt.Errorf("%w: reference %q requires distinct references", ErrSourceConflict, requirement.Ref)
				}
			} else {
				uses[requirement.Ref] = usage{provider: provider, environment: requirement.Environment}
			}
			if providerTargets[provider] == nil {
				providerTargets[provider] = make(map[domain.TargetID]struct{})
			}
			providerTargets[provider][target.ID()] = struct{}{}
		}
	}
	for ref, environment := range explicit {
		if !ref.Valid() || !ValidEnvironmentName(environment) {
			return nil, fmt.Errorf("invalid explicit credential mapping")
		}
		if _, exists := uses[ref]; !exists {
			return nil, fmt.Errorf("explicit credential reference %q is not required by the plan", ref)
		}
	}
	for ref, environment := range configured {
		id := domain.CredentialRef(ref)
		if !id.Valid() || !ValidEnvironmentName(environment) {
			return nil, fmt.Errorf("invalid configured source for reference %q", ref)
		}
		if _, exists := uses[id]; !exists {
			return nil, fmt.Errorf("configured credential reference %q is not required by the plan", ref)
		}
	}
	refs := make([]domain.CredentialRef, 0, len(uses))
	for ref := range uses {
		refs = append(refs, ref)
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i] < refs[j] })
	sources := make(map[domain.CredentialRef]string, len(refs))
	owners := make(map[string]domain.CredentialRef)
	for _, ref := range refs {
		use := uses[ref]
		source, selected := explicit[ref]
		if !selected {
			source, selected = configured[string(ref)]
		}
		if !selected && trustedProviders[use.provider] && len(providerTargets[use.provider]) == 1 {
			source = defaultSource(use.provider, use.environment)
		}
		if source == "" {
			continue // Driver preparation blocks missing credentials before side effects.
		}
		if other, exists := owners[source]; exists && other != ref {
			return nil, fmt.Errorf("%w: references %q and %q select the same environment source", ErrAmbiguousSource, other, ref)
		}
		sources[ref] = source
		owners[source] = ref
	}
	return sources, nil
}

// No generic GITHUB_TOKEN/GH_TOKEN, npm trusted-publishing ID token, or
// provider-subprocess environment value is implicitly inherited.
func defaultSource(provider, requiredEnvironment string) string {
	switch provider + "/" + requiredEnvironment {
	case "npm/DISTROPLANE_NPM_TOKEN":
		return "NPM_TOKEN"
	case "sdkman/DISTROPLANE_SDKMAN_VENDOR_EMAIL":
		return "SDKMAN_VENDOR_EMAIL"
	case "sdkman/DISTROPLANE_SDKMAN_VENDOR_PASSWORD":
		return "SDKMAN_VENDOR_PASSWORD"
	case "sdkman/DISTROPLANE_SDKMAN_CONSUMER_KEY":
		return "SDKMAN_CONSUMER_KEY"
	case "sdkman/DISTROPLANE_SDKMAN_CONSUMER_TOKEN":
		return "SDKMAN_CONSUMER_TOKEN"
	case "homebrew/DISTROPLANE_HOMEBREW_TOKEN":
		return "HOMEBREW_GITHUB_TOKEN"
	case "winget/DISTROPLANE_WINGET_TOKEN":
		return "WINGET_GITHUB_TOKEN"
	}
	return ""
}
