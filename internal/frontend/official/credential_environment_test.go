package official

import "testing"

func TestCredentialEnvironment(t *testing.T) {
	cases := []struct {
		name, provider, field, mode, want string
	}{
		{"npm token", "npm", "credential", "token", "DISTROPLANE_NPM_TOKEN"},
		{"npm default", "npm", "credential", "", "DISTROPLANE_NPM_TOKEN"},
		{"npm trusted publishing", "npm", "credential", "trusted-publishing", "DISTROPLANE_NPM_ID_TOKEN"},
		{"homebrew", "homebrew", "credential", "", "DISTROPLANE_HOMEBREW_TOKEN"},
		{"winget", "winget", "credential", "", "DISTROPLANE_WINGET_TOKEN"},
		{"sdkman state email", "sdkman", "emailCredential", "state", "DISTROPLANE_SDKMAN_VENDOR_EMAIL"},
		{"sdkman state password", "sdkman", "passwordCredential", "state", "DISTROPLANE_SDKMAN_VENDOR_PASSWORD"},
		{"sdkman legacy key", "sdkman", "consumerKeyCredential", "legacy", "DISTROPLANE_SDKMAN_CONSUMER_KEY"},
		{"sdkman legacy token", "sdkman", "consumerTokenCredential", "legacy", "DISTROPLANE_SDKMAN_CONSUMER_TOKEN"},
		{"custom provider", "custom", "credential", "token", ""},
		{"unknown field", "npm", "token", "token", ""},
		{"cross provider field", "winget", "emailCredential", "state", ""},
		{"case mismatch", "NPM", "credential", "token", ""},
		{"missing provider", "", "credential", "token", ""},
		{"missing field", "sdkman", "", "state", ""},
		{"sdkman generic credential", "sdkman", "credential", "legacy", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CredentialEnvironment(tc.provider, tc.field, tc.mode); got != tc.want {
				t.Fatalf("credential environment = %q, want %q", got, tc.want)
			}
		})
	}
}
