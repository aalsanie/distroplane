package providerhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aalsanie/distroplane/internal/credentials"
	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/executor"
	"github.com/aalsanie/distroplane/internal/journal"
	"github.com/aalsanie/distroplane/internal/protocol"
)

const (
	credentialACanary = "distroplane-canary-alpha-9f3d2a"
	credentialBCanary = "distroplane-canary-beta-7c1e4b"
)

type credentialResolverFunc func(context.Context, domain.CredentialRef) (*credentials.Material, error)

func (f credentialResolverFunc) Resolve(ctx context.Context, ref domain.CredentialRef) (*credentials.Material, error) {
	return f(ctx, ref)
}

func providerRefNamed(t testing.TB, name string) domain.ProviderRef {
	t.Helper()
	providerName, err := domain.NewProviderName(name)
	if err != nil {
		t.Fatal(err)
	}
	version, _ := domain.NewProviderVersion("1.0.0")
	ref, err := domain.NewProviderRef(providerName, version)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func credentialRequirement(t testing.TB, ref, environment string) domain.Requirement {
	t.Helper()
	value, err := domain.NewRequirement("credential", ref, []byte(`{"environment":"`+environment+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func copyHelperExecutable(t testing.TB, name string) string {
	t.Helper()
	source := helperExecutable(t)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	destination := filepath.Join(t.TempDir(), name)
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, data, 0o700); err != nil {
		t.Fatal(err)
	}
	return destination
}

func credentialPlan(t testing.TB, providers map[string]domain.ProviderRef) domain.Plan {
	t.Helper()
	digest, _ := domain.NewSHA256Digest(strings.Repeat("a", 64))
	artifact, _ := domain.NewArtifact("app", filepath.Join(t.TempDir(), "app"), digest, 1, "")
	releaseID, _ := domain.NewReleaseID("release-1")
	release, _ := domain.NewRelease(releaseID, []domain.Artifact{artifact})
	configuration, _ := domain.NewJSONValue([]byte(`{}`))
	payloadA, _ := domain.NewJSONValue([]byte(`{"mode":"credential","expectedEnvironment":"CREDENTIAL_A","forbiddenEnvironment":"CREDENTIAL_B"}`))
	payloadB, _ := domain.NewJSONValue([]byte(`{"mode":"credential","expectedEnvironment":"CREDENTIAL_B","forbiddenEnvironment":"CREDENTIAL_A"}`))
	targetAID, _ := domain.NewTargetID("target-a")
	targetBID, _ := domain.NewTargetID("target-b")
	targetA, err := domain.NewTargetWithRequirements(targetAID, providers["provider-a"], configuration, []domain.Requirement{credentialRequirement(t, "credential-a", "CREDENTIAL_A")})
	if err != nil {
		t.Fatal(err)
	}
	targetB, err := domain.NewTargetWithRequirements(targetBID, providers["provider-b"], configuration, []domain.Requirement{credentialRequirement(t, "credential-b", "CREDENTIAL_B")})
	if err != nil {
		t.Fatal(err)
	}
	opAID, _ := domain.NewOperationID("op-a")
	opBID, _ := domain.NewOperationID("op-b")
	opA, err := domain.NewOperation(opAID, targetAID, providers["provider-a"], "publish", nil, true, "key-a", 10*time.Second, payloadA)
	if err != nil {
		t.Fatal(err)
	}
	opB, err := domain.NewOperation(opBID, targetBID, providers["provider-b"], "publish", nil, true, "key-b", 10*time.Second, payloadB)
	if err != nil {
		t.Fatal(err)
	}
	planID, _ := domain.NewPlanID("plan-credentials")
	plan, err := domain.NewPlan(planID, "1", "1", release, []domain.Target{targetA, targetB}, []domain.Operation{opA, opB})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestCredentialIsolationAcrossProviderProcesses(t *testing.T) {
	t.Setenv("CREDENTIAL_A", "ambient-a-must-not-leak")
	t.Setenv("CREDENTIAL_B", "ambient-b-must-not-leak")
	providers := map[string]domain.ProviderRef{
		"provider-a": providerRefNamed(t, "provider-a"),
		"provider-b": providerRefNamed(t, "provider-b"),
	}
	resolver, err := credentials.NewStaticResolver(map[domain.CredentialRef][]byte{
		domain.CredentialRef("credential-a"): []byte(credentialACanary),
		domain.CredentialRef("credential-b"): []byte(credentialBCanary),
	})
	if err != nil {
		t.Fatal(err)
	}
	client := helperClient(t, "binding_identity", nil)
	driver, err := NewDriverWithCredentials(client, resolver, []Binding{
		{Provider: providers["provider-a"], Executable: copyHelperExecutable(t, "provider-a")},
		{Provider: providers["provider-b"], Executable: copyHelperExecutable(t, "provider-b")},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan := credentialPlan(t, providers)
	runID, _ := domain.NewRunID("run-credentials")
	path := filepath.Join(t.TempDir(), "run.journal")
	writer, err := journal.OpenWriter(path, runID)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := executor.New(driver, executor.Options{MaxConcurrency: 2, MaxAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	state, err := engine.Execute(context.Background(), plan, runID, writer)
	if err != nil {
		t.Fatal(err)
	}
	events := writer.Events()
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if !state.Completed {
		t.Fatalf("state=%+v", state)
	}
	for _, operation := range state.Operations() {
		if operation.State != domain.StatePublished {
			t.Fatalf("operation=%+v", operation)
		}
		if strings.Contains(string(operation.Evidence), credentialACanary) || strings.Contains(string(operation.Evidence), credentialBCanary) {
			t.Fatalf("evidence leaked credential: %s", operation.Evidence)
		}
	}
	rawJournal, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{credentialACanary, credentialBCanary} {
		if strings.Contains(string(rawJournal), secret) || strings.Contains(fmt.Sprintf("%+v", plan), secret) {
			t.Fatalf("secret %q persisted", secret)
		}
	}
	credentialEvents := 0
	for _, event := range events {
		if event.Type == journal.EventCredentialResolved {
			credentialEvents++
			if event.Payload.CredentialRef != "credential-a" && event.Payload.CredentialRef != "credential-b" {
				t.Fatalf("event=%+v", event)
			}
		}
	}
	if credentialEvents != 2 {
		t.Fatalf("credential events=%d", credentialEvents)
	}
}

func credentialDriverAndRequest(t testing.TB, mode string) (*Driver, executor.Request) {
	t.Helper()
	ref := testProviderRef(t)
	resolver, err := credentials.NewStaticResolver(map[domain.CredentialRef][]byte{
		domain.CredentialRef("release"): []byte(credentialACanary),
	})
	if err != nil {
		t.Fatal(err)
	}
	driver, err := NewDriverWithCredentials(helperClient(t, "normal", nil), resolver, []Binding{{Provider: ref, Executable: helperExecutable(t)}})
	if err != nil {
		t.Fatal(err)
	}
	request := testExecutorRequest(t, mode, true, nil)
	request.Requirements = []domain.Requirement{credentialRequirement(t, "release", "CREDENTIAL_A")}
	payload, _ := domain.NewJSONValue([]byte(`{"mode":"` + mode + `","expectedEnvironment":"CREDENTIAL_A"}`))
	op := request.Operation
	operation, err := domain.NewOperation(op.ID(), op.TargetID(), op.Provider(), op.Kind(), op.Dependencies(), op.SideEffecting(), op.IdempotencyKey(), op.Timeout(), payload)
	if err != nil {
		t.Fatal(err)
	}
	request.Operation = operation
	return driver, request
}

func TestCredentialRedactionAcrossProviderOutputs(t *testing.T) {
	driver, request := credentialDriverAndRequest(t, "secret_error")
	_, err := driver.Apply(context.Background(), request)
	var driverErr *executor.DriverError
	if !errors.As(err, &driverErr) || strings.Contains(err.Error(), credentialACanary) || !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("err=%v", err)
	}

	driver, request = credentialDriverAndRequest(t, "secret_evidence")
	result, err := driver.Apply(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.ProviderState, credentialACanary) || strings.Contains(string(result.Evidence), credentialACanary) {
		t.Fatalf("result leaked secret: %+v", result)
	}
	if !strings.Contains(result.ProviderState, "[REDACTED]") || !strings.Contains(string(result.Evidence), "[REDACTED]") {
		t.Fatalf("result was not redacted: %+v", result)
	}

	driver, request = credentialDriverAndRequest(t, "secret_crash")
	_, err = driver.Apply(context.Background(), request)
	var processErr *ProcessError
	if !errors.As(err, &processErr) {
		t.Fatalf("err=%v", err)
	}
	if strings.Contains(processErr.Diagnostics, credentialACanary) || !strings.Contains(processErr.Diagnostics, "[REDACTED]") {
		t.Fatalf("diagnostics=%q", processErr.Diagnostics)
	}
}

func TestCredentialPreparationIsSingleUseAndFailsClosed(t *testing.T) {
	driver, request := credentialDriverAndRequest(t, "credential")
	preparation, err := driver.Prepare(context.Background(), request, executor.DriverApply)
	if err != nil {
		t.Fatal(err)
	}
	if len(preparation.CredentialRefs) != 1 || preparation.CredentialRefs[0] != "release" {
		t.Fatalf("preparation=%+v", preparation.CredentialRefs)
	}
	if _, err := driver.Apply(preparation.Context, request); err != nil {
		t.Fatal(err)
	}
	if _, err := driver.Apply(preparation.Context, request); err == nil {
		t.Fatal("prepared credential material was reused")
	}
	preparation.Release()
	preparation.Release()

	withoutResolver, err := NewDriver(helperClient(t, "normal", nil), []Binding{{Provider: testProviderRef(t), Executable: helperExecutable(t)}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := withoutResolver.Prepare(context.Background(), request, executor.DriverApply); err == nil {
		t.Fatal("missing resolver accepted")
	}
	if _, err := NewDriverWithCredentials(helperClient(t, "normal", nil), nil, nil); err == nil {
		t.Fatal("nil credential resolver accepted")
	}

	bad := request
	bad.Requirements = []domain.Requirement{credentialRequirement(t, "release", "CREDENTIAL_A"), credentialRequirement(t, "other", "CREDENTIAL_A")}
	if _, err := driver.Prepare(context.Background(), bad, executor.DriverApply); err == nil {
		t.Fatal("duplicate credential environment accepted")
	}
	bad.Requirements = []domain.Requirement{credentialRequirement(t, "release", "CREDENTIAL_A"), credentialRequirement(t, "release", "CREDENTIAL_B")}
	if _, err := driver.Prepare(context.Background(), bad, executor.DriverApply); err == nil {
		t.Fatal("duplicate credential reference accepted")
	}
	network, _ := domain.NewRequirement("network", "registry", nil)
	bad = request
	bad.Requirements = append([]domain.Requirement{network}, request.Requirements...)
	if _, err := driver.Prepare(context.Background(), bad, executor.DriverApply); err != nil {
		t.Fatal(err)
	}
	if _, err := driver.Prepare(context.Background(), request, 99); err == nil {
		t.Fatal("invalid driver operation accepted")
	}
}

func TestCredentialEnvironmentCollisionsFailClosed(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	ref := testProviderRef(t)
	resolver, err := credentials.NewStaticResolver(map[domain.CredentialRef][]byte{
		domain.CredentialRef("release"): []byte(credentialACanary),
		domain.CredentialRef("other"):   []byte(credentialBCanary),
	})
	if err != nil {
		t.Fatal(err)
	}
	driver, err := NewDriverWithCredentials(
		helperClient(t, "normal", nil),
		resolver,
		[]Binding{{Provider: ref, Executable: helperExecutable(t)}},
	)
	if err != nil {
		t.Fatal(err)
	}
	request := testExecutorRequest(t, "credential", true, nil)
	collisionName := "PATH"
	if runtime.GOOS == "windows" {
		collisionName = "Path"
	}
	request.Requirements = []domain.Requirement{credentialRequirement(t, "release", collisionName)}
	if _, err := driver.Prepare(context.Background(), request, executor.DriverApply); err == nil {
		t.Fatal("credential was allowed to replace provider baseline environment")
	} else {
		var driverErr *executor.DriverError
		if !errors.As(err, &driverErr) || driverErr.Code != "CREDENTIAL_ENVIRONMENT_INVALID" {
			t.Fatalf("err=%v", err)
		}
	}

	explicitClient := helperClient(t, "normal", func(options *Options) {
		options.Environment = append(options.Environment, "DISTROPLANE_EXPLICIT=explicit")
	})
	explicitDriver, err := NewDriverWithCredentials(
		explicitClient,
		resolver,
		[]Binding{{Provider: ref, Executable: helperExecutable(t)}},
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Requirements = []domain.Requirement{credentialRequirement(t, "release", "DISTROPLANE_EXPLICIT")}
	if _, err := explicitDriver.Prepare(context.Background(), request, executor.DriverApply); err == nil {
		t.Fatal("credential was allowed to replace explicit provider environment")
	}

	if runtime.GOOS == "windows" {
		request.Requirements = []domain.Requirement{
			credentialRequirement(t, "release", "DISTROPLANE_CASE_TEST"),
			credentialRequirement(t, "other", "distroplane_case_test"),
		}
		if _, err := driver.Prepare(context.Background(), request, executor.DriverApply); err == nil {
			t.Fatal("case-insensitive credential environment collision accepted")
		}
	}
}

func TestCredentialResolverFailuresAreSanitized(t *testing.T) {
	ref := testProviderRef(t)
	client := helperClient(t, "normal", nil)
	request := testExecutorRequest(t, "credential", true, nil)
	request.Requirements = []domain.Requirement{credentialRequirement(t, "release", "CREDENTIAL_A")}

	for name, resolver := range map[string]credentials.Resolver{
		"not found": credentialResolverFunc(func(context.Context, domain.CredentialRef) (*credentials.Material, error) {
			return nil, credentials.ErrNotFound
		}),
		"opaque failure": credentialResolverFunc(func(context.Context, domain.CredentialRef) (*credentials.Material, error) {
			return nil, errors.New("resolver failed with " + credentialACanary)
		}),
	} {
		t.Run(name, func(t *testing.T) {
			driver, err := NewDriverWithCredentials(client, resolver, []Binding{{Provider: ref, Executable: helperExecutable(t)}})
			if err != nil {
				t.Fatal(err)
			}
			_, err = driver.Prepare(context.Background(), request, executor.DriverApply)
			if err == nil || strings.Contains(err.Error(), credentialACanary) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestRedactorAndEnvironmentMerge(t *testing.T) {
	r := newRedactor([][]byte{[]byte("abc"), nil, []byte("abc"), []byte("abcdef")})
	if got := r.text("x=abcdef,abc!"); got != "x=[REDACTED],[REDACTED]!" {
		t.Fatalf("got=%q", got)
	}
	raw := r.json(json.RawMessage(`{"nested":["abc",1,true],"abcdef":"abc"}`))
	if strings.Contains(string(raw), "abcdef") || strings.Contains(string(raw), `"abc"`) {
		t.Fatalf("raw=%s", raw)
	}
	if got := string(r.json(json.RawMessage(`{`))); got != "{" {
		t.Fatalf("invalid json changed: %q", got)
	}
	if _, err := mergeEnvironment([]string{"A=1"}, []string{"A=2"}); err == nil {
		t.Fatal("environment collision accepted")
	}
	if _, err := mergeEnvironment(nil, []string{"A=1", "A=2"}); err == nil {
		t.Fatal("duplicate extra environment accepted")
	}
	if runtime.GOOS == "windows" {
		if _, err := mergeEnvironment([]string{"Path=1"}, []string{"PATH=2"}); err == nil {
			t.Fatal("case-insensitive environment collision accepted")
		}
		if _, err := mergeEnvironment(nil, []string{"Token=1", "TOKEN=2"}); err == nil {
			t.Fatal("case-insensitive duplicate environment accepted")
		}
	}
	empty, err := mergeEnvironment(nil, nil)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty environment=%v err=%v", empty, err)
	}
	if _, err := mergeEnvironment(nil, []string{"bad"}); err == nil {
		t.Fatal("invalid environment accepted")
	}
	merged, err := mergeEnvironment([]string{"B=2"}, []string{"A=1"})
	if err != nil || fmt.Sprint(merged) != "[A=1 B=2]" {
		t.Fatalf("merged=%v err=%v", merged, err)
	}
	var nilClient *Client
	if _, err := nilClient.processEnvironment(nil); err == nil {
		t.Fatal("nil client accepted")
	}
	redactProviderError(nil, r)
	redactDestination(&protocol.DescribeResponse{}, r)
}
