package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/aalsanie/distroplane/internal/canonicaljson"
)

type optionRule struct {
	kind     string
	required bool
	children map[string]optionRule
}

func str(required bool) optionRule { return optionRule{kind: "string", required: required} }
func boolean() optionRule          { return optionRule{kind: "boolean"} }
func object(required bool, fields map[string]optionRule) optionRule {
	return optionRule{kind: "object", required: required, children: fields}
}

var authRules = map[string]optionRule{
	"mode": str(false), "credential": str(false), "emailCredential": str(false),
	"passwordCredential": str(false), "consumerKeyCredential": str(false), "consumerTokenCredential": str(false),
}
var commitRules = map[string]optionRule{"message": str(false), "authorName": str(false), "authorEmail": str(false)}
var prRules = map[string]optionRule{
	"repository": str(false), "api": str(false), "headOwner": str(false), "title": str(false),
	"body": str(false), "allowInsecureHTTP": boolean(),
}

func optionFields(provider string) map[string]optionRule {
	switch provider {
	case "npm":
		return map[string]optionRule{
			"packagePath": str(true), "registry": str(true), "tag": str(false),
			"access": str(false), "allowInsecureHTTP": boolean(),
			"authentication": object(true, authRules),
		}
	case "sdkman":
		return map[string]optionRule{
			"candidate": str(true), "version": str(true), "url": str(true),
			"vendor": str(false), "platform": str(false), "api": str(false), "stateApi": str(false),
			"stateDistribution": str(false), "checksums": {kind: "stringMap"},
			"allowInsecureHTTP": boolean(), "authentication": object(true, authRules),
		}
	case "homebrew":
		return map[string]optionRule{
			"repository": str(true), "branch": str(false), "path": str(true),
			"mode": str(true), "updateBranch": str(false),
			"manifest": object(true, map[string]optionRule{
				"type": str(true), "name": str(true), "description": str(true),
				"homepage": str(true), "license": str(false), "url": str(true), "version": str(true),
				"binary": str(false), "installKind": str(false), "installSource": str(false),
				"installLayout": str(false), "javaVersion": str(false),
			}),
			"commit": object(false, commitRules), "authentication": object(false, authRules),
			"pullRequest": object(false, prRules),
		}
	case "winget":
		return map[string]optionRule{
			"repository": str(true), "branch": str(false), "updateBranch": str(false),
			"manifestRoot": str(false), "manifestVersion": str(false),
			"package": object(true, map[string]optionRule{
				"identifier": str(true), "version": str(true), "defaultLocale": str(false),
				"publisher": str(true), "name": str(true), "license": str(true),
				"shortDescription": str(true), "packageUrl": str(false), "publisherUrl": str(false),
				"releaseNotesUrl": str(false),
			}),
			"installer": object(true, map[string]optionRule{
				"architecture": str(true), "type": str(true), "url": str(true),
				"archivePath": str(false), "nestedInstallerType": str(false),
				"nestedInstallerFiles": {kind: "array", children: map[string]optionRule{
					"relativeFilePath": str(true), "portableCommandAlias": str(false),
				}},
			}),
			"commit": object(false, commitRules), "authentication": object(true, authRules),
			"pullRequest": object(true, prRules),
		}
	}
	return nil
}

func validateOptionObject(path string, raw json.RawMessage, fields map[string]optionRule) (map[string]json.RawMessage, error) {
	var values map[string]json.RawMessage
	if len(raw) == 0 || raw[0] != '{' || json.Unmarshal(raw, &values) != nil {
		return nil, fmt.Errorf("%s: expected object", path)
	}
	for key := range values {
		if _, ok := fields[key]; !ok {
			return nil, fmt.Errorf("%s.%s: unknown field", path, key)
		}
	}
	keys := make([]string, 0, len(fields))
	for name := range fields {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	for _, key := range keys {
		rule := fields[key]
		raw, exists := values[key]
		field := path + "." + key
		if !exists {
			if rule.required {
				return nil, fmt.Errorf("%s: required field is missing", field)
			}
			continue
		}
		switch rule.kind {
		case "string":
			var value string
			if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &value) != nil {
				return nil, fmt.Errorf("%s: expected string", field)
			}
			if err := validateText(field, value, !rule.required); err != nil {
				return nil, err
			}
		case "boolean":
			var value bool
			if err := json.Unmarshal(raw, &value); err != nil {
				return nil, fmt.Errorf("%s: expected boolean", field)
			}
		case "object":
			if _, err := validateOptionObject(field, raw, rule.children); err != nil {
				return nil, err
			}
		case "stringMap":
			var value map[string]string
			if len(raw) == 0 || raw[0] != '{' || json.Unmarshal(raw, &value) != nil {
				return nil, fmt.Errorf("%s: expected map of strings", field)
			}
			for k, v := range value {
				if err := validateText(field+"."+k, k, false); err != nil {
					return nil, err
				}
				if err := validateText(field+"."+k, v, false); err != nil {
					return nil, err
				}
			}
		case "array":
			var values []json.RawMessage
			if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &values) != nil {
				return nil, fmt.Errorf("%s: expected array", field)
			}
			for i, item := range values {
				if _, err := validateOptionObject(fmt.Sprintf("%s[%d]", field, i), item, rule.children); err != nil {
					return nil, err
				}
			}
		}
	}
	return values, nil
}

func requireV2(path string, values map[string]json.RawMessage, names ...string) error {
	current := values
	for i, name := range names {
		raw, ok := current[name]
		if !ok {
			return fmt.Errorf("%s.%s: required field is missing", path, strings.Join(names[:i+1], "."))
		}
		if i == len(names)-1 {
			var value string
			if json.Unmarshal(raw, &value) != nil || value == "" {
				return fmt.Errorf("%s.%s: expected non-empty value", path, strings.Join(names, "."))
			}
		} else {
			var nested map[string]json.RawMessage
			if err := json.Unmarshal(raw, &nested); err != nil {
				return fmt.Errorf("%s.%s: expected object", path, strings.Join(names[:i+1], "."))
			}
			current = nested
		}
	}
	return nil
}
func optionString(value json.RawMessage) string {
	var s string
	_ = json.Unmarshal(value, &s)
	return s
}
func optionBool(value json.RawMessage) bool { var b bool; _ = json.Unmarshal(value, &b); return b }

func normalizeV2Options(provider string, raw json.RawMessage, artifact, path string) (json.RawMessage, error) {
	options, err := validateOptionObject(path, raw, optionFields(provider))
	if err != nil {
		return nil, err
	}
	var auth map[string]json.RawMessage
	if v, ok := options["authentication"]; ok {
		_ = json.Unmarshal(v, &auth)
	}
	switch provider {
	case "npm":
		for _, name := range []string{"mode", "credential"} {
			if err := requireV2(path, options, "authentication", name); err != nil {
				return nil, err
			}
		}
		if err := validLocalPath(path+".packagePath", optionString(options["packagePath"])); err != nil {
			return nil, err
		}
	case "sdkman":
		if err := requireV2(path, options, "authentication", "mode"); err != nil {
			return nil, err
		}
		names := []string{}
		switch optionString(auth["mode"]) {
		case "state":
			names = []string{"emailCredential", "passwordCredential"}
		case "legacy":
			names = []string{"consumerKeyCredential", "consumerTokenCredential"}
		default:
			return nil, fmt.Errorf("%s.authentication.mode: expected state or legacy", path)
		}
		for _, name := range names {
			if err := requireV2(path, options, "authentication", name); err != nil {
				return nil, err
			}
		}
	case "homebrew":
		mode := optionString(options["mode"])
		if mode != "direct" && mode != "pull-request" {
			return nil, fmt.Errorf("%s.mode: expected direct or pull-request", path)
		}
		if mode == "pull-request" {
			if err := requireV2(path, options, "pullRequest", "repository"); err != nil {
				return nil, err
			}
		}
		var manifest map[string]json.RawMessage
		_ = json.Unmarshal(options["manifest"], &manifest)
		switch optionString(manifest["type"]) {
		case "formula":
			if err := requireV2(path, options, "manifest", "binary"); err != nil {
				return nil, err
			}
		case "cask":
			for _, name := range []string{"installKind", "installSource"} {
				if err := requireV2(path, options, "manifest", name); err != nil {
					return nil, err
				}
			}
		default:
			return nil, fmt.Errorf("%s.manifest.type: expected formula or cask", path)
		}
	case "winget":
		if err := requireV2(path, options, "authentication", "credential"); err != nil {
			return nil, err
		}
		if err := requireV2(path, options, "pullRequest", "repository"); err != nil {
			return nil, err
		}
	}
	allowHTTP := optionBool(options["allowInsecureHTTP"])
	for _, key := range []string{"url", "registry", "api", "stateApi"} {
		if value, ok := options[key]; ok {
			if err := validateV2URL(path+"."+key, optionString(value), allowHTTP); err != nil {
				return nil, err
			}
		}
	}
	for _, section := range []string{"manifest", "installer"} {
		if val, ok := options[section]; ok {
			var child map[string]json.RawMessage
			_ = json.Unmarshal(val, &child)
			for _, name := range []string{"url", "homepage"} {
				if raw, ok := child[name]; ok {
					if err := validateV2URL(path+"."+section+"."+name, optionString(raw), false); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	// The artifact association is a v2 core field, never user-defined provider data.
	options["artifact"], err = json.Marshal(artifact)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(options)
	if err != nil {
		return nil, err
	}
	return canonicaljson.Normalize(encoded)
}

func validateV2URL(field, value string, allowHTTP bool) error {
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || (u.Scheme != "https" && (u.Scheme != "http" || !allowHTTP)) {
		return fmt.Errorf("%s: expected absolute HTTPS URL without credentials (HTTP requires explicit allowInsecureHTTP)", field)
	}
	return nil
}
