package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"time"

	"github.com/aalsanie/distroplane/internal/config"
	"github.com/aalsanie/distroplane/internal/credentials"
	"github.com/aalsanie/distroplane/internal/planner"
	"github.com/aalsanie/distroplane/internal/providerhost"
	"github.com/aalsanie/distroplane/internal/protocol"
	"github.com/aalsanie/distroplane/internal/runstate"
)

type doctorCheck struct {
	ID       string `json:"id"`
	Severity string `json:"severity"`
	Subject  string `json:"subject"`
	Message  string `json:"message"`
	Fix      string `json:"fix,omitempty"`
}

type doctorOutput struct {
	OutputSchemaVersion string        `json:"outputSchemaVersion"`
	Ready               bool          `json:"ready"`
	Checks              []doctorCheck `json:"checks"`
}

type doctorProbe func(context.Context, string) error

func runDoctorContext(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	var configPath string
	var networkCheck, jsonMode bool
	fs := commandFlags("doctor")
	fs.StringVar(&configPath, "config", "distroplane.json", "")
	fs.BoolVar(&networkCheck, "network", false, "")
	fs.BoolVar(&jsonMode, "json", false, "")
	if err := parseCommand(fs, args); err != nil {
		return writeCommandError(stderr, hasJSON(args), exitUsage, "usage", err)
	}
	out := diagnose(ctx, configPath, networkCheck, publicDoctorProbe)
	code := exitOK
	for _, check := range out.Checks {
		if check.Severity == "FAIL" {
			code = exitInvalid
			break
		}
	}
	if jsonMode {
		if result := writeJSON(stdout, out); result != exitOK {
			return result
		}
		return code
	}
	for _, check := range out.Checks {
		fmt.Fprintf(stdout, "%s %s [%s]: %s", check.Severity, check.ID, check.Subject, check.Message)
		if check.Fix != "" {
			fmt.Fprintf(stdout, "\n  Next: %s", check.Fix)
		}
		fmt.Fprintln(stdout)
	}
	return code
}

func diagnose(ctx context.Context, path string, probeNetwork bool, probe doctorProbe) doctorOutput {
	out := doctorOutput{OutputSchemaVersion: outputSchemaVersion, Ready: true, Checks: []doctorCheck{}}
	add := func(id, severity, subject, message, fix string) {
		if severity == "FAIL" {
			out.Ready = false
		}
		out.Checks = append(out.Checks, doctorCheck{ID: id, Severity: severity, Subject: subject, Message: message, Fix: fix})
	}
	if err := ctx.Err(); err != nil {
		add("CANCELLED", "FAIL", "execution", "diagnostic inspection was cancelled", "Retry doctor when the cancellation has cleared.")
		return out
	}
	add("CLI_RUNTIME", "PASS", "cli", fmt.Sprintf("%s/%s; version %s", runtime.GOOS, runtime.GOARCH, version), "")
	loaded, err := config.Load(path)
	if err != nil {
		id, fix := "INVALID_CONFIG", "Correct the configuration JSON and schema before publishing."
		if errors.Is(err, os.ErrNotExist) {
			id, fix = "MISSING_CONFIG", "Generate a configuration with distroplane init or provide --config PATH."
		}
		add(id, "FAIL", "config", "configuration cannot be loaded or validated", fix)
		add("NETWORK_NOT_CHECKED", "SKIP", "network", "remote access not checked", "Fix local configuration first.")
		return out
	}
	add("CONFIG_VALID", "PASS", "config", fmt.Sprintf("schema %s; %d destination(s)", loaded.Config.SchemaVersion, len(loaded.Config.Targets)), "")
	if loaded.Config.SchemaVersion == config.SchemaVersion {
		add("CONFIG_V1", "WARN", "config", "schema v1 remains supported; no automatic conversion or rewrite occurs", "Use distroplane init to generate a new v2 file separately if desired.")
	}
	for _, artifact := range loaded.Config.Release.Artifacts {
		if err := ctx.Err(); err != nil {
			add("CANCELLED", "FAIL", "execution", "diagnostic inspection was cancelled", "Retry doctor.")
			return out
		}
		source := artifact.Source
		if !filepath.IsAbs(source) {
			source = filepath.Join(loaded.BaseDir, source)
		}
		info, err := os.Lstat(source)
		switch {
		case errors.Is(err, os.ErrNotExist):
			add("MISSING_ARTIFACT", "FAIL", "artifact:"+artifact.Name, "artifact file is missing", "Build the artifact or correct release.artifacts source.")
		case err != nil:
			add("UNREADABLE_ARTIFACT", "FAIL", "artifact:"+artifact.Name, "artifact cannot be inspected", "Check local file access and path.")
		case !info.Mode().IsRegular() || info.Size() == 0:
			add("INVALID_ARTIFACT", "FAIL", "artifact:"+artifact.Name, "artifact must be a nonempty regular file", "Replace it with the intended immutable build output.")
		default:
			file, openErr := os.Open(source)
			if openErr != nil {
				add("UNREADABLE_ARTIFACT", "FAIL", "artifact:"+artifact.Name, "artifact cannot be opened", "Check read permission.")
				continue
			}
			hash := sha256.New()
			n, hashErr := io.Copy(hash, file)
			closeErr := file.Close()
			if hashErr != nil || closeErr != nil || n != info.Size() {
				add("UNREADABLE_ARTIFACT", "FAIL", "artifact:"+artifact.Name, "artifact cannot be read consistently", "Rebuild the artifact and rerun doctor.")
			} else {
				add("ARTIFACT_VALID", "PASS", "artifact:"+artifact.Name, "readable SHA-256 "+hex.EncodeToString(hash.Sum(nil)), "")
			}
		}
	}
	requiresGit := false
	providers := make(map[string]bool)
	for _, target := range loaded.Config.Targets {
		name := target.Provider.Name
		providers[name] = true
		subject := "provider:" + target.ID
		executable, err := planner.ResolveExecutable(loaded.BaseDir, name, target.Provider.Executable)
		if err != nil {
			add("PROVIDER_NOT_FOUND", "FAIL", subject, "provider executable not available or unsafe", "Install the matching provider binary or correct destinations executable.")
		} else {
			file, err := os.Open(executable)
			if err != nil {
				add("PROVIDER_UNREADABLE", "FAIL", subject, "provider executable cannot be read", "Restore a readable provider executable.")
			} else {
				hash := sha256.New()
				_, copyErr := io.Copy(hash, file)
				closeErr := file.Close()
				if copyErr != nil || closeErr != nil {
					add("PROVIDER_UNREADABLE", "FAIL", subject, "provider executable cannot be hashed", "Restore the provider executable.")
				} else {
					add("PROVIDER_VALID", "PASS", subject, fmt.Sprintf("executable digest SHA-256 %s", hex.EncodeToString(hash.Sum(nil))), "")
					probeProviderIdentity(ctx, name, executable, subject, add)
				}
			}
		}
		if name == "homebrew" || name == "winget" {
			requiresGit = true
		}
		checkDoctorCredentials(target, loaded.Config.CredentialSources, add)
	}
	if requiresGit {
		if _, err := exec.LookPath("git"); err != nil {
			add("GIT_NOT_FOUND", "FAIL", "git", "Git executable is unavailable", "Install Git and ensure it is on PATH.")
		} else {
			add("GIT_FOUND", "PASS", "git", "Git executable found on PATH; remote permissions not checked", "")
		}
	} else {
		add("GIT_NOT_REQUIRED", "SKIP", "git", "no Git-backed destination configured", "")
	}
	root := filepath.Join(loaded.BaseDir, ".distroplane")
	records, err := (runstate.Store{Root: root}).Inventory()
	if err != nil {
		add("STATE_CORRUPT", "FAIL", "state", "run catalog or retained plan/journal cannot be verified", "Preserve the state files; inspect and recover them without creating a new run.")
	} else {
		add("STATE_VALID", "PASS", "state", fmt.Sprintf("%d verified plan record(s)", len(records)), "")
		for _, record := range records {
			if record.State == "RESERVED" {
				add("RUN_RESERVED", "WARN", "run:"+record.RunID, "run is reserved but its journal is missing", "Investigate durable state before resuming; never replace a lost journal.")
			}
			if record.ReconcileRequired {
				add("RECONCILE_REQUIRED", "WARN", "run:"+record.RunID, "external reconciliation is required", "Use distroplane resume --reconcile-only for this run.")
			}
		}
	}
	if !probeNetwork {
		add("NETWORK_NOT_CHECKED", "SKIP", "network", "remote connectivity and authentication have not been checked", "Rerun with --network for limited public endpoint checks.")
		return out
	}
	if probe == nil {
		probe = publicDoctorProbe
	}
	known := map[string]string{
		"npm": "https://registry.npmjs.org/",
		"sdkman": "https://state.sdkman.io/",
		"homebrew": "https://api.github.com/",
		"winget": "https://api.github.com/",
	}
	names := make([]string, 0, len(providers))
	for p := range providers { names = append(names, p) }
	sort.Strings(names)
	seen := make(map[string]bool)
	for _, name := range names {
		endpoint, ok := known[name]
		if !ok {
			add("NETWORK_UNSUPPORTED", "SKIP", "network:"+name, "no supported public endpoint probe", "")
			continue
		}
		if seen[endpoint] { continue }
		seen[endpoint] = true
		if err := probe(ctx, endpoint); err != nil {
			add("NETWORK_UNREACHABLE", "WARN", "network:"+name, "public endpoint did not respond; repository-specific access unverified", "Check network policy, DNS, or proxy; rerun doctor --network.")
		} else {
			add("NETWORK_REACHABLE", "PASS", "network:"+name, "public endpoint responded; publication rights and configured remote URL were not checked", "")
		}
	}
	return out
}

func checkDoctorCredentials(target config.Target, configured map[string]string, add func(string, string, string, string, string)) {
	var raw map[string]any
	if err := json.Unmarshal(target.Configuration, &raw); err != nil {
		add("CREDENTIAL_UNKNOWN", "SKIP", "credential:"+target.ID, "credential requirements could not be inspected", "Validate destination options.")
		return
	}
	auth, _ := raw["authentication"].(map[string]any)
	if auth == nil {
		return
	}
	fields := []string{"credential", "emailCredential", "passwordCredential", "consumerKeyCredential", "consumerTokenCredential"}
	defaults := map[string]string{
		"npm/credential": "NPM_TOKEN",
		"sdkman/emailCredential": "SDKMAN_VENDOR_EMAIL",
		"sdkman/passwordCredential": "SDKMAN_VENDOR_PASSWORD",
		"sdkman/consumerKeyCredential": "SDKMAN_CONSUMER_KEY",
		"sdkman/consumerTokenCredential": "SDKMAN_CONSUMER_TOKEN",
		"homebrew/credential": "HOMEBREW_GITHUB_TOKEN",
		"winget/credential": "WINGET_GITHUB_TOKEN",
	}
	for _, field := range fields {
		ref, _ := auth[field].(string)
		if ref == "" { continue }
		env := configured[ref]
		if env == "" {
			env = defaults[target.Provider.Name+"/"+field]
			if target.Provider.Executable != "" {
				env = "" // Explicit binaries never inherit ambient defaults.
			}
			if target.Provider.Name == "npm" && auth["mode"] == "trusted-publishing" {
				env = "" // Never guess an OIDC ID token.
			}
		}
		subject := "credential:" + target.ID + "/" + field
		if env == "" {
			add("MISSING_CREDENTIAL", "FAIL", subject, "no explicit environment source is configured; provider binding may be blocked", "Supply --credential REF=ENV or a scoped credentialSources mapping.")
			continue
		}
		if !credentials.ValidEnvironmentName(env) {
			add("MISSING_CREDENTIAL", "WARN", subject, "configured environment name is invalid", "Correct the credentialSources mapping.")
			continue
		}
		value, ok := os.LookupEnv(env)
		if !ok || value == "" {
			add("MISSING_CREDENTIAL", "WARN", subject, "required credential environment variable is absent or empty", "Set the specified environment variable before publication; do not put credential bytes in config.")
		} else {
			add("CREDENTIAL_AVAILABLE", "PASS", subject, "credential source environment variable exists; permission was not tested", "")
		}
	}
}

func publicDoctorProbe(ctx context.Context, endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return fmt.Errorf("invalid public endpoint")
	}
	// Probes are restricted to exact built-in hosts and never include secrets.
	switch u.Hostname() {
	case "registry.npmjs.org", "state.sdkman.io", "api.github.com":
	default:
		return fmt.Errorf("endpoint not allowed")
	}
	bounded, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(bounded, http.MethodHead, endpoint, nil)
	if err != nil { return err }
	client := &http.Client{
		Timeout: 3*time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{Timeout: 2*time.Second}).DialContext,
			TLSHandshakeTimeout: 2*time.Second,
			DisableKeepAlives: true,
		},
	}
	resp, err := client.Do(req)
	if err != nil { return err }
	defer resp.Body.Close()
	if resp.StatusCode >= 500 { return fmt.Errorf("remote endpoint unavailable") }
	return nil
}

func probeProviderIdentity(ctx context.Context, name, executable, subject string, add func(string, string, string, string, string)) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	client, err := providerhost.New(providerhost.Options{})
	if err != nil {
		add("PROVIDER_UNAVAILABLE", "FAIL", subject, "provider protocol client cannot start", "Check the local runtime and provider installation.")
		return
	}
	response, err := client.Describe(ctx, planner.Endpoint{Name: name, Executable: executable})
	if err != nil {
		add("PROVIDER_UNAVAILABLE", "FAIL", subject, "provider describe handshake failed", "Install a compatible provider executable and verify its architecture.")
		return
	}
	if response.Provider.Name != name {
		add("VERSION_MISMATCH", "FAIL", subject, "provider identity does not match the configured destination", "Choose the matching provider binary; do not reuse an unrelated executable.")
		return
	}
	compatible := false
	for _, offered := range response.ProtocolVersions {
		if offered == protocol.Version {
			compatible = true
		}
	}
	if !compatible {
		add("VERSION_MISMATCH", "FAIL", subject, "provider protocol version is incompatible", "Install a provider that supports the current protocol.")
		return
	}
	add("PROVIDER_VERSION", "PASS", subject, "provider identity and protocol version match; provider version "+response.Provider.Version, "")
}
