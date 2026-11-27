package main

import (
	"context"
	"debug/buildinfo"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/aalsanie/distroplane/internal/config"
	"github.com/aalsanie/distroplane/internal/credentials"
	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/frontend/official"
	"github.com/aalsanie/distroplane/internal/planner"
	"github.com/aalsanie/distroplane/internal/protocol"
	"github.com/aalsanie/distroplane/internal/providerhost"
)

const (
	doctorPass = "PASS"
	doctorWarn = "WARN"
	doctorFail = "FAIL"
	doctorSkip = "SKIP"
)

type doctorFinding struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Subject  string `json:"subject"`
	Message  string `json:"message"`
	Fix      string `json:"fix,omitempty"`
}

type doctorOutput struct {
	OutputSchemaVersion string          `json:"outputSchemaVersion"`
	Healthy             bool            `json:"healthy"`
	Checks              []doctorFinding `json:"checks"`
}

type doctorChecks struct {
	output          doctorOutput
	localBad        bool
	remoteBad       bool
	planCredentials map[string][]doctorCredential
}

func (d *doctorChecks) add(code, severity, subject, message, fix string) {
	d.output.Checks = append(d.output.Checks, doctorFinding{
		Code: code, Severity: severity, Subject: subject, Message: message, Fix: fix,
	})
	if severity == doctorFail {
		if strings.HasPrefix(code, "REMOTE_") {
			d.remoteBad = true
		} else {
			d.localBad = true
		}
	}
}

func (d *doctorChecks) exit() int {
	if d.localBad {
		return exitInvalid
	}
	if d.remoteBad {
		return exitOperational
	}
	return exitOK
}

type doctorProbe struct {
	remote      bool
	timeout     time.Duration
	planPath    string
	credentials credentialFlags
	dial        func(context.Context, string, string) (net.Conn, error)
}

func runDoctorContext(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	var configPath, planPath string
	var jsonMode, remote bool
	var timeout time.Duration
	mappings := credentialFlags{}
	fs := commandFlags("doctor")
	fs.StringVar(&configPath, "config", "distroplane.json", "")
	fs.StringVar(&planPath, "plan", "", "")
	fs.BoolVar(&jsonMode, "json", false, "")
	fs.BoolVar(&remote, "remote", false, "")
	fs.DurationVar(&timeout, "timeout", 3*time.Second, "")
	fs.Var(mappings, "credential", "")
	if err := parseCommand(fs, args); err != nil {
		return writeCommandError(stderr, hasJSON(args), exitUsage, "usage", err)
	}
	if timeout <= 0 || timeout > 30*time.Second {
		return writeCommandError(stderr, jsonMode, exitUsage, "usage", fmt.Errorf("--timeout must be between 1ns and 30s"))
	}
	if err := ctx.Err(); err != nil {
		return writeCommandError(stderr, jsonMode, exitOperational, "cancelled", err)
	}
	probe := doctorProbe{remote: remote, timeout: timeout, planPath: planPath, credentials: mappings, dial: doctorSafeDial}
	report := inspectDoctor(ctx, configPath, probe)
	report.output.Healthy = !report.localBad && !report.remoteBad
	if jsonMode {
		if code := writeJSON(stdout, report.output); code != exitOK {
			return code
		}
		return report.exit()
	}
	writeDoctorHuman(stdout, configPath, remote, report.output)
	return report.exit()
}

func inspectDoctor(ctx context.Context, configPath string, options doctorProbe) doctorChecks {
	d := doctorChecks{output: doctorOutput{OutputSchemaVersion: outputSchemaVersion, Checks: []doctorFinding{}}}
	d.add("CLI_RUNTIME", doctorPass, "cli", fmt.Sprintf("CLI %s on %s/%s", version, runtime.GOOS, runtime.GOARCH), "")
	if err := ctx.Err(); err != nil {
		d.add("CANCELLED", doctorFail, "runtime", "diagnostics cancelled", "Run doctor again once cancellation ends")
		return d
	}
	loaded, err := official.Load(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			d.add("MISSING_CONFIG", doctorFail, "config", "configuration file not found", "Create a config using distroplane init or select --config")
		} else {
			d.add("INVALID_CONFIG", doctorFail, "config", "configuration does not pass strict validation", "Fix the configuration using distroplane validate --config PATH")
		}
		d.add("DEPENDENCY_SKIPPED", doctorSkip, "providers", "requires valid configuration", "")
		d.add("REMOTE_SKIPPED", doctorSkip, "network", "requires valid configuration", "")
		return d
	}
	d.add("CONFIG_VALID", doctorPass, "config", fmt.Sprintf("schema %s; %d destinations", loaded.Config.SchemaVersion, len(loaded.Config.Targets)), "")
	if loaded.Config.SchemaVersion == config.SchemaVersion {
		d.add("SCHEMA_V1", doctorWarn, "config", "schema 1 remains supported; configuration was not modified", "For new configurations, use distroplane init to create schema 2")
	} else {
		d.add("SCHEMA_V2", doctorPass, "config", "strict schema 2", "")
	}
	for _, a := range loaded.Config.Release.Artifacts {
		select {
		case <-ctx.Done():
			d.add("CANCELLED", doctorFail, "artifacts", "diagnostics cancelled", "")
			return d
		default:
		}
		path := a.Source
		if !filepath.IsAbs(path) {
			path = filepath.Join(loaded.BaseDir, path)
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			d.add("MISSING_ARTIFACT", doctorFail, a.Name, "artifact is missing, empty, or not a regular file", "Rebuild or select an existing regular artifact source")
			continue
		}
		if _, _, err := (planner.FileHasher{}).Hash(path); err != nil {
			d.add("ARTIFACT_HASH_FAILED", doctorFail, a.Name, "artifact bytes cannot be hashed consistently", "Rebuild the artifact and verify file access")
		} else {
			d.add("ARTIFACT_READABLE", doctorPass, a.Name, "local artifact is present, nonempty, and hashable", "")
		}
	}
	bindings, trustedDefaults, probeProviders := doctorPlanBindings(&d, loaded, options.planPath)
	if !probeProviders {
		d.add("DEPENDENCY_SKIPPED", doctorSkip, "providers", "saved provider selection or integrity is unverified", "")
		d.add("REMOTE_SKIPPED", doctorSkip, "network", "requires verified provider selection", "")
		return d
	}
	digestsMatch := bindings != nil
	providerCounts := map[string]int{}
	for _, target := range loaded.Config.Targets {
		providerCounts[target.Provider.Name]++
	}
	endpoints := make(map[planner.Endpoint]bool)
	credentialOwners := make(map[string]string)
	credentialPurposes := make(map[string]string)
	requiredReferences := make(map[string]bool)
	client, clientErr := providerhost.New(providerhost.Options{})
	for _, target := range loaded.Config.Targets {
		if err := ctx.Err(); err != nil {
			d.add("CANCELLED", doctorFail, "providers", "diagnostics cancelled", "")
			return d
		}
		binding := bindings[target.ID]
		executable := binding.Executable
		trusted := trustedDefaults[target.Provider.Name]
		var err error
		if bindings == nil {
			executable, err = planner.ResolveExecutable(loaded.BaseDir, target.Provider.Name, target.Provider.Executable)
			trusted = err == nil && target.Provider.Executable == "" && planner.IsTrustedInstalledProvider(target.Provider.Name, executable)
		}
		if err != nil {
			d.add("PROVIDER_NOT_FOUND", doctorFail, target.ID, "provider "+safeDisplay(target.Provider.Name)+" executable is missing or unsafe", "Install the matching distroplane-provider-"+safeDisplay(target.Provider.Name)+" beside the CLI or correct the configured executable path")
			d.add("CREDENTIAL_SKIPPED", doctorSkip, target.ID, "provider identity not verified", "")
			continue
		}
		file, err := os.Stat(executable)
		if err != nil || !file.Mode().IsRegular() {
			digestsMatch = false
			d.add("PROVIDER_NOT_FOUND", doctorFail, target.ID, "provider executable could not be verified", "Restore the selected provider executable")
			continue
		}
		d.add("PROVIDER_FOUND", doctorPass, target.ID, "provider executable verified as a regular file", "")
		actual, _, err := (planner.FileHasher{}).Hash(executable)
		if err != nil {
			digestsMatch = false
			d.add("PROVIDER_HASH_FAILED", doctorFail, target.ID, "provider executable cannot be hashed reliably", "Restore readable provider executable bytes")
			continue
		}
		if bindings != nil && actual != binding.Digest {
			digestsMatch = false
			d.add("PROVIDER_DIGEST_CHANGED", doctorFail, target.ID, "retained provider bytes differ from the immutable plan", "Restore the exact retained provider from a trusted backup")
			continue
		}
		d.add("PROVIDER_HASHED", doctorPass, target.ID, "provider executable digest computed locally", "")
		compatibleArch := true
		if build, err := buildinfo.ReadFile(executable); err == nil {
			var osValue, arch string
			for _, setting := range build.Settings {
				if setting.Key == "GOOS" {
					osValue = setting.Value
				}
				if setting.Key == "GOARCH" {
					arch = setting.Value
				}
			}
			if osValue == "" || arch == "" {
				d.add("ARCH_UNKNOWN", doctorWarn, target.ID, "provider architecture metadata is incomplete", "Verify host architecture compatibility")
			} else if osValue != runtime.GOOS || arch != runtime.GOARCH {
				compatibleArch = false
				d.add("ARCH_MISMATCH", doctorFail, target.ID, "provider architecture does not match this host", "Use the host operating system and architecture required by the selected provider")
			} else {
				d.add("ARCH_VERIFIED", doctorPass, target.ID, "provider architecture is compatible with the host", "")
			}
		} else {
			d.add("ARCH_UNKNOWN", doctorWarn, target.ID, "binary does not expose Go build architecture metadata", "Confirm provider compatibility with this host")
		}
		endpoint := planner.Endpoint{Name: target.Provider.Name, Executable: executable}
		if !compatibleArch {
			d.add("VERSION_UNVERIFIED", doctorSkip, target.ID, "provider architecture is incompatible", "")
		} else if !endpoints[endpoint] {
			endpoints[endpoint] = true
			if clientErr != nil {
				d.add("VERSION_UNVERIFIED", doctorFail, target.ID, "provider client unavailable", "Repair CLI installation")
			} else {
				callCtx, cancel := context.WithTimeout(ctx, options.timeout)
				describe, err := client.Describe(callCtx, endpoint)
				cancel()
				if err != nil {
					d.add("VERSION_MISMATCH", doctorFail, target.ID, "provider Describe protocol failed or timed out", "Check the selected provider at "+executable)
				} else if describe.Provider.Name != target.Provider.Name || describe.Provider.Version == "" ||
					(bindings != nil && describe.Provider.Version != string(binding.Provider.Version())) {
					d.add("VERSION_MISMATCH", doctorFail, target.ID, "provider identity or version is incompatible", "Check the provider at "+executable+" against the selected CLI or saved plan")
				} else if !doctorSupportsProtocol(describe.ProtocolVersions) {
					d.add("VERSION_MISMATCH", doctorFail, target.ID, "provider does not advertise the required protocol version", "Install a provider compatible with the CLI protocol")
				} else {
					d.add("PROVIDER_VERSION", doctorPass, target.ID, "provider identity and protocol verified; version "+describe.Provider.Version, "")
				}
			}
		}
		if target.Provider.Name == "homebrew" || target.Provider.Name == "winget" {
			if _, err := exec.LookPath("git"); err != nil {
				d.add("GIT_MISSING", doctorFail, target.ID, "Git executable not available", "Install Git and ensure it is on PATH")
			} else {
				d.add("GIT_AVAILABLE", doctorPass, target.ID, "Git command found on PATH", "")
			}
		}
		requirements := doctorCredentialRefs(target)
		if bindings != nil {
			requirements = d.planCredentials[target.ID]
		}
		for _, credential := range requirements {
			requiredReferences[credential.ref] = true
			purpose := target.Provider.Name + "/" + credential.environment
			if previous, exists := credentialPurposes[credential.ref]; exists && previous != purpose {
				d.add("CREDENTIAL_COLLISION", doctorFail, target.ID+"/"+credential.ref, "credential reference has incompatible destination purposes", "Use distinct credential references for distinct destinations")
				continue
			}
			credentialPurposes[credential.ref] = purpose
			source := doctorCredentialSource(credential.ref, target.Provider.Name, credential.environment, loaded.Config.CredentialSources, options.credentials, providerCounts[target.Provider.Name] == 1 && trusted)
			if source == "" {
				d.add("MISSING_CREDENTIAL", doctorFail, target.ID+"/"+credential.ref, "credential reference has no approved environment source", "Provide --credential "+safeDisplay(credential.ref)+"=ENV or configure credentialSources with an isolated source")
			} else if previous, taken := credentialOwners[strings.ToUpper(source)]; taken && previous != credential.ref {
				d.add("CREDENTIAL_COLLISION", doctorFail, target.ID+"/"+credential.ref, "credential references "+safeDisplay(previous)+" and "+safeDisplay(credential.ref)+" map to "+safeDisplay(source), "Assign a separate environment name per credential reference")
			} else if value, present := os.LookupEnv(source); !present || value == "" {
				d.add("MISSING_CREDENTIAL", doctorFail, target.ID+"/"+credential.ref, "credential environment source "+safeDisplay(source)+" is unset or empty", "Set "+safeDisplay(source)+" before publishing")
			} else {
				d.add("CREDENTIAL_AVAILABLE", doctorPass, target.ID+"/"+credential.ref, "mapped credential environment source is populated (value not displayed; authorization not verified)", "")
			}
			if source != "" {
				credentialOwners[strings.ToUpper(source)] = credential.ref
			}
		}
	}
	for ref, source := range options.credentials {
		if !credentials.ValidEnvironmentName(source) {
			d.add("INVALID_CREDENTIAL_MAPPING", doctorFail, string(ref), "environment mapping name is invalid", "Provide --credential REF=ENV using a valid environment variable name")
		}
		if !requiredReferences[string(ref)] {
			d.add("UNUSED_CREDENTIAL_MAPPING", doctorFail, string(ref), "credential reference is not declared by this configuration", "Remove the mapping or correct the reference name")
		}
	}
	if digestsMatch {
		d.add("PROVIDER_DIGEST_MATCH", doctorPass, "plan", "retained provider executable bytes match the immutable plan", "")
	}
	if options.remote {
		if options.dial == nil {
			options.dial = doctorSafeDial
		}
		urls := doctorRemoteURLs(loaded)
		if len(urls) == 0 {
			d.add("REMOTE_SKIPPED", doctorSkip, "network", "no recognized destination hosts for a safe connectivity probe", "")
		}
		for _, remoteURL := range urls {
			if err := ctx.Err(); err != nil {
				d.add("CANCELLED", doctorFail, "network", "diagnostics cancelled", "")
				return d
			}
			address, err := doctorRemoteAddress(remoteURL)
			if err != nil {
				d.add("REMOTE_INVALID", doctorFail, "network", "remote endpoint is invalid or non-HTTPS", "Use an HTTPS endpoint for destination metadata")
				continue
			}
			dialCtx, cancel := context.WithTimeout(ctx, options.timeout)
			conn, err := options.dial(dialCtx, "tcp", address)
			cancel()
			if err != nil {
				d.add("REMOTE_UNREACHABLE", doctorFail, "network", "destination TCP endpoint is unreachable; no authentication or publication attempted", "Check DNS, firewall, and proxy access; external acceptance is not verified")
			} else {
				_ = conn.Close()
				d.add("REMOTE_REACHABLE", doctorPass, "network", "destination TCP endpoint reachable; remote authorization and publication not verified", "")
			}
		}
	} else {
		d.add("REMOTE_SKIPPED", doctorSkip, "network", "remote connectivity not checked", "Use --remote for opt-in HTTPS TCP connectivity probes")
	}
	return d
}

type doctorCredential struct {
	ref         string
	environment string
}

func doctorCredentialRefs(target config.Target) []doctorCredential {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(target.Configuration, &root); err != nil {
		return nil
	}
	var auth map[string]json.RawMessage
	if err := json.Unmarshal(root["authentication"], &auth); err != nil {
		return nil
	}
	var mode string
	_ = json.Unmarshal(auth["mode"], &mode)
	var names []doctorCredential
	for _, key := range []string{"credential", "emailCredential", "passwordCredential", "consumerKeyCredential", "consumerTokenCredential"} {
		if value, exists := auth[key]; exists {
			var name string
			if json.Unmarshal(value, &name) == nil && name != "" {
				environment := official.CredentialEnvironment(target.Provider.Name, key, mode)
				names = append(names, doctorCredential{ref: name, environment: environment})
			}
		}
	}
	sort.Slice(names, func(i, j int) bool { return names[i].ref < names[j].ref })
	return names
}

func doctorCredentialSource(ref, provider, environment string, configured map[string]string, explicit credentialFlags, trusted bool) string {
	if value := explicit[domain.CredentialRef(ref)]; value != "" && credentials.ValidEnvironmentName(value) {
		return value
	}
	if value := configured[ref]; value != "" && credentials.ValidEnvironmentName(value) {
		return value
	}
	if !trusted {
		return ""
	}
	return official.DefaultSource(provider, environment)
}

func doctorRemoteURLs(loaded config.Loaded) []string {
	seen := make(map[string]bool)
	for _, target := range loaded.Config.Targets {
		var options map[string]json.RawMessage
		if json.Unmarshal(target.Configuration, &options) != nil {
			continue
		}
		for _, key := range []string{"registry", "url", "api", "stateApi", "repository"} {
			var value string
			if json.Unmarshal(options[key], &value) == nil && strings.HasPrefix(value, "https://") {
				seen[value] = true
			}
		}
		for _, key := range []string{"manifest", "installer"} {
			var nested map[string]json.RawMessage
			if json.Unmarshal(options[key], &nested) != nil {
				continue
			}
			var value string
			if json.Unmarshal(nested["url"], &value) == nil && strings.HasPrefix(value, "https://") {
				seen[value] = true
			}
		}
	}
	urls := make([]string, 0, len(seen))
	for remote := range seen {
		urls = append(urls, remote)
	}
	sort.Strings(urls)
	return urls
}

func doctorRemoteAddress(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || !doctorPublicHost(u.Hostname()) || u.User != nil {
		return "", fmt.Errorf("invalid or non-public HTTPS endpoint")
	}
	port := u.Port()
	if port == "" {
		port = "443"
	}
	return net.JoinHostPort(u.Hostname(), port), nil
}

func doctorSupportsProtocol(versions []string) bool {
	for _, version := range versions {
		if version == protocol.Version {
			return true
		}
	}
	return false
}
