package config

import (
	"reflect"
	"strings"
	"testing"
)

const credentialSourcesFixture = `{"schemaVersion":"2","release":{"id":"release","artifacts":{"app":{"source":"dist/app.tgz"}}},"destinations":{"npm":{"artifact":"app","options":{"packagePath":"dist/app.tgz","registry":"https://registry.npmjs.org/","authentication":{"mode":"token","credential":"npm-publish"}}}}}`

func TestV2CredentialSourcesOnlyCarryEnvironmentNames(t *testing.T) {
	without, err := Decode(strings.NewReader(credentialSourcesFixture))
	if err != nil { t.Fatal(err) }
	with, err := Decode(strings.NewReader(strings.Replace(credentialSourcesFixture,
		`"destinations":`, `"credentialSources":{"npm-publish":"RELEASE_TOKEN"},"destinations":`, 1)))
	if err != nil { t.Fatal(err) }
	if with.CredentialSources["npm-publish"] != "RELEASE_TOKEN" { t.Fatalf("sources=%v", with.CredentialSources) }
	if !reflect.DeepEqual(with.Release, without.Release) || !reflect.DeepEqual(with.Targets, without.Targets) {
		t.Fatal("environment source changed semantic release intent")
	}
}

func TestV2CredentialSourcesRejectInvalidValues(t *testing.T) {
	cases := []string{
		`"credentialSources":null,`,
		`"credentialSources":[],`,
		`"credentialSources":"NPM_TOKEN",`,
		`"credentialSources":{"npm-publish":null},`,
		`"credentialSources":{"npm-publish":23},`,
		`"credentialSources":{"npm-publish":"BAD-ENV"},`,
		`"credentialSources":{"npm-publish":""},`,
		`"credentialSources":{"":"VALID_NAME"},`,
		`"credentialSources":{"npm-publish":"VALID_NAME","npm-publish":"OTHER_NAME"},`,
	}
	for _, item := range cases {
		t.Run(item, func(t *testing.T) {
			raw := strings.Replace(credentialSourcesFixture, `"destinations":`, item+`"destinations":`, 1)
			if _, err := Decode(strings.NewReader(raw)); err == nil {
				t.Fatal("invalid environment source was accepted")
			}
		})
	}
}
