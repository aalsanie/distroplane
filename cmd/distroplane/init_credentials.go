package main

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/aalsanie/distroplane/internal/config"
	"github.com/aalsanie/distroplane/internal/credentials"
	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/frontend/official"
)

type initCredentialSource struct {
	Reference   string `json:"reference"`
	Environment string `json:"environment,omitempty"`
	Explicit    bool   `json:"explicit"`
}

func initSources(cfg config.Config, mappings credentialFlags) ([]initCredentialSource, error) {
	sources := []initCredentialSource{}
	used := map[domain.CredentialRef]bool{}
	owners := map[string]string{}
	for _, target := range cfg.Targets {
		for _, requirement := range doctorCredentialRefs(target) {
			ref := domain.CredentialRef(requirement.ref)
			source, explicit := mappings[ref]
			used[ref] = true
			if explicit && !credentials.ValidEnvironmentName(source) {
				return nil, fmt.Errorf("credential %s: use --credential REF=ENV with an environment variable name, not a value", ref)
			}
			if !explicit {
				source = official.DefaultSource(target.Provider.Name, requirement.environment)
			}
			if source != "" {
				key := strings.ToUpper(source)
				if previous, exists := owners[key]; exists && previous != requirement.ref {
					return nil, fmt.Errorf("credential references %s and %s require distinct environment sources", previous, requirement.ref)
				}
				owners[key] = requirement.ref
			}
			sources = append(sources, initCredentialSource{Reference: requirement.ref, Environment: source, Explicit: explicit})
		}
	}
	for ref := range mappings {
		if !used[ref] {
			return nil, fmt.Errorf("credential mapping does not refer to a selected destination requirement")
		}
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].Reference < sources[j].Reference })
	return sources, nil
}

func writeInitSummary(w io.Writer, result initOutput) {
	for _, variant := range result.Destinations {
		provider, _, _ := strings.Cut(variant, ":")
		artifact := result.Artifacts[provider]
		fmt.Fprintf(w, "Destination %s: %s (%d bytes)\n", variant, safeDisplay(artifact.Path), artifact.Size)
	}
	for _, source := range result.CredentialSources {
		if source.Environment == "" {
			fmt.Fprintf(w, "Credential %s: select an isolated environment name with --credential %s=ENV when running doctor and publish\n", safeDisplay(source.Reference), safeDisplay(source.Reference))
			continue
		}
		fmt.Fprintf(w, "Credential %s: set environment variable %s (value not collected)\n", safeDisplay(source.Reference), safeDisplay(source.Environment))
	}
}
