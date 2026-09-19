package evidence

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/journal"
)

func TestBuildMixedBundleAndDeterministicSerialization(t *testing.T) {
	plan := evidencePlan(t)
	raw := evidenceJournal(t, plan)
	attestations := []AttestationReference{
		{Name: "workflow", URI: "https://github.com/example/repo/attestations/2?token=attestation-secret"},
		{Name: "build", URI: "urn:example:build:1", Digest: "sha256:" + strings.Repeat("c", 64)},
		{Name: "workflow", URI: "https://github.com/example/repo/attestations/2?token=attestation-secret"},
	}
	bundle, err := Build(plan, raw, "run.journal", attestations)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.SchemaVersion != SchemaVersion || bundle.PlanID != string(plan.ID()) || bundle.ProtocolVersion != plan.ProtocolVersion() {
		t.Fatalf("bundle=%+v", bundle)
	}
	if bundle.Run.ID != "run-evidence" || bundle.Run.Completed || bundle.Run.Cancelled {
		t.Fatalf("run=%+v", bundle.Run)
	}
	if bundle.ObservedAt.IsZero() || bundle.Run.StartedAt.IsZero() || bundle.Run.StartedAt.After(bundle.ObservedAt) ||
		bundle.Run.CompletedAt != nil || bundle.Run.CancelledAt != nil {
		t.Fatalf("observation metadata bundle=%s run=%+v", bundle.ObservedAt, bundle.Run)
	}
	if bundle.Release.ID != "release-evidence" || len(bundle.Release.Artifacts) != 2 {
		t.Fatalf("release=%+v", bundle.Release)
	}
	if bundle.Release.Artifacts[0].Name != "app" || bundle.Release.Artifacts[1].Name != "symbols" {
		t.Fatalf("artifacts=%+v", bundle.Release.Artifacts)
	}
	if bundle.Journal.Reference != "run.journal" || bundle.Journal.Events != 9 || bundle.Journal.Bytes != int64(len(raw)) ||
		bundle.Journal.SchemaVersion != journal.SchemaVersion || !strings.HasPrefix(bundle.Journal.Digest, "sha256:") {
		t.Fatalf("journal=%+v", bundle.Journal)
	}
	if len(bundle.Attestations) != 2 || bundle.Attestations[0].Name != "build" || bundle.Attestations[1].Name != "workflow" {
		t.Fatalf("attestations=%+v", bundle.Attestations)
	}
	if strings.Contains(bundle.Attestations[1].URI, "attestation-secret") || !strings.Contains(bundle.Attestations[1].URI, "%5BREDACTED%5D") {
		t.Fatalf("attestation URI=%q", bundle.Attestations[1].URI)
	}

	states := map[string]string{}
	for _, target := range bundle.Targets {
		states[target.ID] = target.State
		if target.Provider.Name != "fake" || target.Provider.Version != "1.0.0" || len(target.Operations) != 1 {
			t.Fatalf("target=%+v", target)
		}
		if target.ObservedAt.IsZero() || target.Operations[0].ObservedAt.IsZero() ||
			!target.ObservedAt.Equal(target.Operations[0].ObservedAt) || target.ObservedAt.After(bundle.ObservedAt) {
			t.Fatalf("target observation=%+v bundleObservedAt=%s", target, bundle.ObservedAt)
		}
	}
	if states["failed"] != string(domain.StateFailed) ||
		states["pending"] != string(domain.StateWaitingExternal) ||
		states["published"] != string(domain.StatePublished) {
		t.Fatalf("states=%+v", states)
	}
	published := targetRecord(t, bundle, "published")
	if len(published.ExternalReferences) != 2 {
		t.Fatalf("published references=%+v", published.ExternalReferences)
	}
	if !containsString(published.ExternalReferences, "https://registry.example.test/pkg") {
		t.Fatalf("published references=%+v", published.ExternalReferences)
	}
	var redactedDownload string
	for _, ref := range published.ExternalReferences {
		if strings.HasPrefix(ref, "https://downloads.example.test/app?") {
			redactedDownload = ref
		}
	}
	if redactedDownload == "" || strings.Contains(redactedDownload, "download-secret") || !strings.Contains(redactedDownload, "%5BREDACTED%5D") {
		t.Fatalf("redacted download=%q", redactedDownload)
	}
	pending := targetRecord(t, bundle, "pending")
	if len(pending.ExternalReferences) != 1 || pending.ExternalReferences[0] != "https://github.com/example/repo/pull/7" ||
		!pending.ReconcileRequired {
		t.Fatalf("pending=%+v", pending)
	}
	failed := targetRecord(t, bundle, "failed")
	if failed.Operations[0].ErrorCode != "PERMANENT_EXTERNAL_ERROR" || len(failed.Operations[0].Evidence) != 0 {
		t.Fatalf("failed=%+v", failed)
	}

	first, err := Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(first) {
		t.Fatalf("invalid bundle JSON: %s", first)
	}
	for _, secret := range []string{"provider-secret", "download-secret", "attestation-secret"} {
		if bytes.Contains(first, []byte(secret)) {
			t.Fatalf("secret %q leaked: %s", secret, first)
		}
	}
	for _, artifact := range plan.Release().Artifacts() {
		if bytes.Contains(first, []byte(artifact.Source())) {
			t.Fatalf("artifact source leaked: %q", artifact.Source())
		}
	}
	secondBundle, err := Build(plan, raw, "run.journal", []AttestationReference{attestations[1], attestations[0]})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Marshal(secondBundle)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("serialization is not deterministic\nfirst=%s\nsecond=%s", first, second)
	}
	if Digest(first) != Digest(second) || !strings.HasPrefix(Digest(first), "sha256:") {
		t.Fatalf("digest mismatch")
	}
}

func TestBuildCompletedObservationMetadata(t *testing.T) {
	plan := singlePublishedPlan(t)
	raw := publishedJournal(t, plan)
	bundle, err := Build(plan, raw, "run.journal", nil)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Run.CompletedAt == nil || !bundle.Run.CompletedAt.Equal(bundle.ObservedAt) {
		t.Fatalf("bundleObservedAt=%s run=%+v", bundle.ObservedAt, bundle.Run)
	}
	if bundle.Run.CancelledAt != nil || bundle.Run.StartedAt.IsZero() || !bundle.Run.StartedAt.Before(bundle.ObservedAt) {
		t.Fatalf("run=%+v", bundle.Run)
	}
	target := bundle.Targets[0]
	if target.ObservedAt.IsZero() || target.Operations[0].ObservedAt.IsZero() ||
		!target.ObservedAt.Equal(target.Operations[0].ObservedAt) || !target.ObservedAt.Before(bundle.ObservedAt) {
		t.Fatalf("target=%+v bundleObservedAt=%s", target, bundle.ObservedAt)
	}
}

func TestDeriveObservationsUsesDurableEventTimes(t *testing.T) {
	start := time.Unix(10, 0).UTC()
	ready := time.Unix(20, 0).UTC()
	dispatched := time.Unix(30, 0).UTC()
	cancelled := time.Unix(40, 0).UTC()
	meta := deriveObservations([]journal.Event{
		{Type: journal.EventRunStarted, ObservedAt: start},
		{Type: journal.EventOperationReady, OperationID: "op-a", ObservedAt: ready},
		{Type: journal.EventProviderProcessStarted, OperationID: "op-a", ObservedAt: time.Unix(25, 0).UTC()},
		{Type: journal.EventSideEffectDispatched, OperationID: "op-a", ObservedAt: dispatched},
		{Type: journal.EventRunCancelled, ObservedAt: cancelled},
	})
	if !meta.startedAt.Equal(start) || !meta.observedAt.Equal(cancelled) || meta.cancelledAt == nil || !meta.cancelledAt.Equal(cancelled) {
		t.Fatalf("meta=%+v", meta)
	}
	if meta.completedAt != nil || !meta.operations["op-a"].Equal(dispatched) {
		t.Fatalf("meta=%+v", meta)
	}
	if operationObservationEvent(journal.EventProviderProcessStarted) {
		t.Fatal("provider process start treated as evidence state observation")
	}
}

func TestBuildUsesDurablePrefixForTruncatedJournal(t *testing.T) {
	plan := singlePublishedPlan(t)
	raw := publishedJournal(t, plan)
	truncated := append(append([]byte(nil), raw...), []byte{'D', 'P', 'J'}...)
	bundle, err := Build(plan, truncated, "run.journal", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bundle.Journal.TruncatedTail || bundle.Journal.Bytes != int64(len(raw)) {
		t.Fatalf("journal=%+v", bundle.Journal)
	}
	want := Digest(raw)
	if bundle.Journal.Digest != want {
		t.Fatalf("digest=%q want=%q", bundle.Journal.Digest, want)
	}
}

func TestBuildRejectsInvalidInputs(t *testing.T) {
	plan := singlePublishedPlan(t)
	raw := publishedJournal(t, plan)
	if _, err := Build(domain.Plan{}, raw, "run.journal", nil); err == nil {
		t.Fatal("zero plan accepted")
	}
	if _, err := Build(plan, nil, "run.journal", nil); err == nil {
		t.Fatal("empty journal accepted")
	}
	if _, err := Build(plan, raw, " run.journal ", nil); err == nil {
		t.Fatal("invalid journal reference accepted")
	}
	corrupt := append([]byte(nil), raw...)
	corrupt[0] = 'X'
	if _, err := Build(plan, corrupt, "run.journal", nil); err == nil {
		t.Fatal("corrupt journal accepted")
	}
	for name, attestation := range map[string]AttestationReference{
		"name":   {Name: "", URI: "urn:test"},
		"uri":    {Name: "build", URI: "relative"},
		"digest": {Name: "build", URI: "urn:test", Digest: "sha256:bad"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Build(plan, raw, "run.journal", []AttestationReference{attestation}); err == nil {
				t.Fatal("invalid attestation accepted")
			}
		})
	}
	bundle, err := Build(plan, raw, "run.journal", nil)
	if err != nil {
		t.Fatal(err)
	}
	bundle.SchemaVersion = "2"
	if _, err := Marshal(bundle); err == nil {
		t.Fatal("unsupported evidence schema accepted")
	}
}

func TestEvidenceSanitizationAndExternalReferences(t *testing.T) {
	raw := json.RawMessage(`{
		"apiKey":"raw-secret",
		"nested":{"Authorization":"auth-secret"},
		"message":"token=message-secret Bearer bearer-secret",
		"url":"https://example.test/path?token=query-secret&ok=1",
		"duplicate":["https://example.test/a","https://example.test/a"],
		"relative":"not-a-url",
		"userInfo":"https://user:password@example.test/private"
	}`)
	normalized, err := normalizeEvidence(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"raw-secret", "auth-secret", "message-secret", "bearer-secret", "query-secret", "password"} {
		if bytes.Contains(normalized, []byte(secret)) {
			t.Fatalf("secret %q leaked: %s", secret, normalized)
		}
	}
	if !bytes.Contains(normalized, []byte(`"apiKey":"[REDACTED]"`)) ||
		!bytes.Contains(normalized, []byte(`"Authorization":"[REDACTED]"`)) {
		t.Fatalf("secret-bearing keys were not redacted: %s", normalized)
	}
	refs := externalReferences(normalized)
	if len(refs) != 2 || !containsString(refs, "https://example.test/a") {
		t.Fatalf("refs=%+v", refs)
	}
	var queryRef string
	for _, ref := range refs {
		if strings.HasPrefix(ref, "https://example.test/path?") {
			queryRef = ref
		}
	}
	if queryRef == "" || strings.Contains(queryRef, "query-secret") {
		t.Fatalf("query ref=%q", queryRef)
	}
	if refs := externalReferences(json.RawMessage(`{`)); refs != nil {
		t.Fatalf("malformed references=%+v", refs)
	}
	if _, err := normalizeEvidence(json.RawMessage(`{"x":1,"x":2}`)); err == nil {
		t.Fatal("duplicate evidence key accepted")
	}
}

func TestSanitizersAndValidationHelpers(t *testing.T) {
	if got := sanitizeText("Authorization=abc"); strings.Contains(got, "abc") {
		t.Fatalf("sanitizeText=%q", got)
	}
	if got := sanitizeText("Basic abcdef"); strings.Contains(got, "abcdef") {
		t.Fatalf("sanitizeText=%q", got)
	}
	uri, err := sanitizeURI("https://user:pass@example.test/path?X-Amz-Signature=secret&ok=1")
	if err != nil || strings.Contains(uri, "pass") || strings.Contains(uri, "secret") {
		t.Fatalf("uri=%q err=%v", uri, err)
	}
	if _, err := sanitizeURI("relative"); err == nil {
		t.Fatal("relative URI accepted")
	}
	for _, key := range []string{"token", "API-Key", "x_amz_signature", "clientSecret", "PRIVATE KEY"} {
		if !secretKey(key) {
			t.Fatalf("secret key not detected: %q", key)
		}
	}
	if secretKey("sha256") {
		t.Fatal("safe key marked secret")
	}
	if err := validateText("x", "", true, 4); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"empty":   "",
		"space":   " x ",
		"control": "x\n",
		"long":    "12345",
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateText("x", value, false, 4); err == nil {
				t.Fatal("invalid text accepted")
			}
		})
	}
	if err := validateText("x", "ok", false, 4); err != nil {
		t.Fatal(err)
	}
}

func evidencePlan(t *testing.T) domain.Plan {
	t.Helper()
	digestA, _ := domain.NewSHA256Digest(strings.Repeat("a", 64))
	digestB, _ := domain.NewSHA256Digest(strings.Repeat("b", 64))
	artifactA, _ := domain.NewArtifact("app", filepath.Join(t.TempDir(), "app.bin"), digestA, 10, "application/octet-stream")
	artifactB, _ := domain.NewArtifact("symbols", filepath.Join(t.TempDir(), "symbols.zip"), digestB, 20, "application/zip")
	releaseID, _ := domain.NewReleaseID("release-evidence")
	release, err := domain.NewRelease(releaseID, []domain.Artifact{artifactB, artifactA})
	if err != nil {
		t.Fatal(err)
	}
	providerName, _ := domain.NewProviderName("fake")
	providerVersion, _ := domain.NewProviderVersion("1.0.0")
	provider, _ := domain.NewProviderRef(providerName, providerVersion)
	configuration, _ := domain.NewJSONValue([]byte(`{}`))
	targetNames := []string{"published", "pending", "failed"}
	targets := make([]domain.Target, 0, len(targetNames))
	operations := make([]domain.Operation, 0, len(targetNames))
	for _, name := range targetNames {
		targetID, _ := domain.NewTargetID(name)
		target, err := domain.NewTarget(targetID, provider, configuration)
		if err != nil {
			t.Fatal(err)
		}
		targets = append(targets, target)
		operationID, _ := domain.NewOperationID("publish-" + name)
		payload, _ := domain.NewJSONValue([]byte(`{"planned":true}`))
		operation, err := domain.NewOperation(operationID, targetID, provider, "publish", nil, true, "key-"+name, 0, payload)
		if err != nil {
			t.Fatal(err)
		}
		operations = append(operations, operation)
	}
	planID, _ := domain.NewPlanID("plan-evidence")
	plan, err := domain.NewPlan(planID, "1", "1", release, targets, operations)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func singlePublishedPlan(t *testing.T) domain.Plan {
	t.Helper()
	plan := evidencePlan(t)
	target := plan.Targets()[0]
	operation := plan.Operations()[0]
	release := plan.Release()
	planID, _ := domain.NewPlanID("plan-single")
	single, err := domain.NewPlan(planID, plan.SchemaVersion(), plan.ProtocolVersion(), release, []domain.Target{target}, []domain.Operation{operation})
	if err != nil {
		t.Fatal(err)
	}
	return single
}

func evidenceJournal(t *testing.T, plan domain.Plan) []byte {
	t.Helper()
	runID, _ := domain.NewRunID("run-evidence")
	path := filepath.Join(t.TempDir(), "run.journal")
	writer, err := journal.OpenWriter(path, runID)
	if err != nil {
		t.Fatal(err)
	}
	appendEntry(t, writer, journal.Entry{RunID: runID, Type: journal.EventRunStarted})
	for _, operation := range plan.Operations() {
		appendEntry(t, writer, journal.Entry{
			RunID: runID, Type: journal.EventAttemptStarted, OperationID: operation.ID(), TargetID: operation.TargetID(),
			Payload: journal.Payload{Attempt: 1},
		})
		switch string(operation.TargetID()) {
		case "published":
			appendEntry(t, writer, journal.Entry{
				RunID: runID, Type: journal.EventSideEffectDispatched, OperationID: operation.ID(), TargetID: operation.TargetID(),
				Payload: journal.Payload{Attempt: 1},
			})
			appendEntry(t, writer, journal.Entry{
				RunID: runID, Type: journal.EventOperationResult, OperationID: operation.ID(), TargetID: operation.TargetID(),
				Payload: journal.Payload{
					Attempt: 1, State: domain.StatePublished, ProviderState: "published",
					Evidence: json.RawMessage(`{"message":"Bearer provider-secret","download":"https://downloads.example.test/app?token=download-secret","packageUrl":"https://registry.example.test/pkg"}`),
				},
			})
		case "pending":
			appendEntry(t, writer, journal.Entry{
				RunID: runID, Type: journal.EventSideEffectDispatched, OperationID: operation.ID(), TargetID: operation.TargetID(),
				Payload: journal.Payload{Attempt: 1},
			})
			appendEntry(t, writer, journal.Entry{
				RunID: runID, Type: journal.EventOperationResult, OperationID: operation.ID(), TargetID: operation.TargetID(),
				Payload: journal.Payload{
					Attempt: 1, State: domain.StateWaitingExternal, ProviderState: "review-pending",
					Evidence: json.RawMessage(`{"pullRequestUrl":"https://github.com/example/repo/pull/7","publicationState":"pending"}`),
				},
			})
		case "failed":
			appendEntry(t, writer, journal.Entry{
				RunID: runID, Type: journal.EventOperationResult, OperationID: operation.ID(), TargetID: operation.TargetID(),
				Payload: journal.Payload{Attempt: 1, State: domain.StateFailed, ErrorCode: "PERMANENT_EXTERNAL_ERROR"},
			})
		default:
			t.Fatalf("unexpected target %q", operation.TargetID())
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func publishedJournal(t *testing.T, plan domain.Plan) []byte {
	t.Helper()
	runID, _ := domain.NewRunID("run-evidence")
	path := filepath.Join(t.TempDir(), "run.journal")
	writer, err := journal.OpenWriter(path, runID)
	if err != nil {
		t.Fatal(err)
	}
	operation := plan.Operations()[0]
	for _, entry := range []journal.Entry{
		{RunID: runID, Type: journal.EventRunStarted},
		{RunID: runID, Type: journal.EventAttemptStarted, OperationID: operation.ID(), TargetID: operation.TargetID(), Payload: journal.Payload{Attempt: 1}},
		{RunID: runID, Type: journal.EventSideEffectDispatched, OperationID: operation.ID(), TargetID: operation.TargetID(), Payload: journal.Payload{Attempt: 1}},
		{RunID: runID, Type: journal.EventOperationResult, OperationID: operation.ID(), TargetID: operation.TargetID(), Payload: journal.Payload{
			Attempt: 1, State: domain.StatePublished, ProviderState: "published", Evidence: json.RawMessage(`{"url":"https://example.test/release"}`),
		}},
		{RunID: runID, Type: journal.EventRunCompleted},
	} {
		appendEntry(t, writer, entry)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func appendEntry(t *testing.T, writer *journal.Writer, entry journal.Entry) {
	t.Helper()
	if _, err := writer.Append(entry); err != nil {
		t.Fatal(err)
	}
}

func targetRecord(t *testing.T, bundle Bundle, id string) TargetRecord {
	t.Helper()
	for _, target := range bundle.Targets {
		if target.ID == id {
			return target
		}
	}
	t.Fatalf("target %q not found", id)
	return TargetRecord{}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
