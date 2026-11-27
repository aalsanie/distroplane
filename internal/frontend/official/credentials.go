package official

import (
	"github.com/aalsanie/distroplane/internal/credentials"
	"github.com/aalsanie/distroplane/internal/domain"
)

func SelectSources(plan domain.Plan, configured map[string]string, explicit map[domain.CredentialRef]string, trustedProviders map[string]bool) (map[domain.CredentialRef]string, error) {
	return credentials.SelectSources(plan, configured, explicit, trustedProviders, credentialCandidates())
}

func credentialCandidates() map[credentials.SourceKey]string {
	return map[credentials.SourceKey]string{
		{Provider: "npm", Environment: "DISTROPLANE_NPM_TOKEN"}:                 "NPM_TOKEN",
		{Provider: "sdkman", Environment: "DISTROPLANE_SDKMAN_VENDOR_EMAIL"}:    "SDKMAN_VENDOR_EMAIL",
		{Provider: "sdkman", Environment: "DISTROPLANE_SDKMAN_VENDOR_PASSWORD"}: "SDKMAN_VENDOR_PASSWORD",
		{Provider: "sdkman", Environment: "DISTROPLANE_SDKMAN_CONSUMER_KEY"}:    "SDKMAN_CONSUMER_KEY",
		{Provider: "sdkman", Environment: "DISTROPLANE_SDKMAN_CONSUMER_TOKEN"}:  "SDKMAN_CONSUMER_TOKEN",
		{Provider: "homebrew", Environment: "DISTROPLANE_HOMEBREW_TOKEN"}:       "HOMEBREW_GITHUB_TOKEN",
		{Provider: "winget", Environment: "DISTROPLANE_WINGET_TOKEN"}:           "WINGET_GITHUB_TOKEN",
	}
}

func DefaultSource(provider, environment string) string {
	return credentialCandidates()[credentials.SourceKey{Provider: provider, Environment: environment}]
}

func CredentialEnvironment(provider, field, mode string) string {
	environment := ""
	switch provider + "/" + field {
	case "npm/credential":
		environment = "DISTROPLANE_NPM_TOKEN"
		if mode == "trusted-publishing" {
			environment = "DISTROPLANE_NPM_ID_TOKEN"
		}
	case "homebrew/credential":
		environment = "DISTROPLANE_HOMEBREW_TOKEN"
	case "winget/credential":
		environment = "DISTROPLANE_WINGET_TOKEN"
	case "sdkman/emailCredential":
		environment = "DISTROPLANE_SDKMAN_VENDOR_EMAIL"
	case "sdkman/passwordCredential":
		environment = "DISTROPLANE_SDKMAN_VENDOR_PASSWORD"
	case "sdkman/consumerKeyCredential":
		environment = "DISTROPLANE_SDKMAN_CONSUMER_KEY"
	case "sdkman/consumerTokenCredential":
		environment = "DISTROPLANE_SDKMAN_CONSUMER_TOKEN"
	}
	return environment
}
