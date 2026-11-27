package credentials

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/aalsanie/distroplane/internal/domain"
)

var (
	ErrAmbiguousSource = errors.New("credential source is ambiguous")
	ErrSourceConflict  = errors.New("credential reference is shared by incompatible destinations")
)

type SourceKey struct {
	Provider    string
	Environment string
}

// SelectSources chooses environment variable names for credential requirements
// from a verified plan. Credential bytes are never read during this step.
func SelectSources(plan domain.Plan, configured map[string]string, explicit map[domain.CredentialRef]string, trustedProviders map[string]bool, candidates map[SourceKey]string) (map[domain.CredentialRef]string, error) {
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
			source = candidates[SourceKey{Provider: use.provider, Environment: use.environment}]
			if source != "" && !ValidEnvironmentName(source) {
				return nil, fmt.Errorf("invalid candidate source for reference %q", ref)
			}
		}
		if source == "" {
			continue // Driver preparation blocks missing credentials before side effects.
		}
		// Windows environment variables are case-insensitive. Reject case-only
		// aliases on all hosts to keep cross-platform source selection safe.
		key := strings.ToUpper(source)
		if other, exists := owners[key]; exists && other != ref {
			return nil, fmt.Errorf("%w: references %q and %q select the same environment source", ErrAmbiguousSource, other, ref)
		}
		sources[ref] = source
		owners[key] = ref
	}
	return sources, nil
}
