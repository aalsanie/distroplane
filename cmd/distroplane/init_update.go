package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/aalsanie/distroplane/internal/config"
	"github.com/aalsanie/distroplane/internal/domain"
	"github.com/aalsanie/distroplane/internal/frontend/official"
	"github.com/aalsanie/distroplane/internal/planner"
	"github.com/aalsanie/distroplane/internal/runstate"
)

type updateDocument struct {
	SchemaVersion string                     `json:"schemaVersion"`
	Release       updateRelease              `json:"release"`
	Destinations  map[string]json.RawMessage `json:"destinations"`
	Sources       json.RawMessage            `json:"credentialSources,omitempty"`
}

type updateRelease struct {
	ID        string                    `json:"id"`
	Artifacts map[string]map[string]any `json:"artifacts"`
}

type updateDestination struct {
	variant, id, artifact string
	entry, options        map[string]any
	version, download     map[string]any
}

type updateChange struct {
	Destination string `json:"destination"`
	OldArtifact string `json:"oldArtifact"`
	NewArtifact string `json:"newArtifact"`
	SHA256      string `json:"sha256"`
	OldVersion  string `json:"oldVersion,omitempty"`
	NewVersion  string `json:"newVersion,omitempty"`
	OldURL      string `json:"oldUrl,omitempty"`
	NewURL      string `json:"newUrl,omitempty"`
}

func readUpdateConfig(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > config.MaxConfigBytes {
		return nil, fmt.Errorf("update requires a regular, bounded configuration file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, fmt.Errorf("configuration changed before reading")
	}
	data, err := io.ReadAll(io.LimitReader(file, config.MaxConfigBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > config.MaxConfigBytes {
		return nil, fmt.Errorf("configuration exceeds size limit")
	}
	return data, nil
}

func updateString(object map[string]any, key string) string {
	value, _ := object[key].(string)
	return value
}

func updateObject(object map[string]any, key string) map[string]any {
	value, _ := object[key].(map[string]any)
	return value
}

func decodeUpdate(data []byte) (updateDocument, []updateDestination, error) {
	cfg, err := official.Decode(bytes.NewReader(data))
	if err != nil {
		return updateDocument{}, nil, err
	}
	if cfg.SchemaVersion != config.SchemaVersionV2 {
		return updateDocument{}, nil, fmt.Errorf("--update requires a guided schema 2 configuration; schema 1 is not migrated")
	}
	var doc updateDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return doc, nil, err
	}
	if doc.Release.ID != cfg.Release.ID || len(doc.Release.Artifacts) != len(cfg.Release.Artifacts) || len(doc.Destinations) != len(cfg.Targets) {
		return doc, nil, fmt.Errorf("--update requires one destination object per provider; arrays are not supported")
	}
	for _, artifact := range cfg.Release.Artifacts {
		if updateString(doc.Release.Artifacts[artifact.Name], "source") != artifact.Source ||
			updateString(doc.Release.Artifacts[artifact.Name], "mediaType") != artifact.MediaType {
			return doc, nil, fmt.Errorf("--update requires canonical artifact field names")
		}
	}
	var destinations []updateDestination
	used := map[string]bool{}
	for _, target := range cfg.Targets {
		provider := target.Provider.Name
		if target.Provider.Executable != "" {
			return doc, nil, fmt.Errorf("--update does not support explicitly configured provider executables")
		}
		var entry map[string]any
		if err := json.Unmarshal(doc.Destinations[provider], &entry); err != nil {
			return doc, nil, fmt.Errorf("--update does not support destination arrays; edit the configuration manually")
		}
		options := updateObject(entry, "options")
		d := updateDestination{variant: provider, id: target.ID, artifact: updateString(entry, "artifact"), entry: entry, options: options}
		if options == nil || doc.Release.Artifacts[d.artifact] == nil || (updateString(entry, "id") != target.ID && !(updateString(entry, "id") == "" && target.ID == provider)) {
			return doc, nil, fmt.Errorf("--update requires canonical destination field names")
		}
		// Refuse configurations that would be reinterpreted by a case-insensitive decoder.
		var normalized map[string]any
		if err := json.Unmarshal(target.Configuration, &normalized); err != nil {
			return doc, nil, err
		}
		if updateString(normalized, "artifact") != d.artifact {
			return doc, nil, fmt.Errorf("--update requires an explicit declared artifact reference")
		}
		switch provider {
		case "npm":
			if updateString(options, "packagePath") != updateString(doc.Release.Artifacts[d.artifact], "source") {
				return doc, nil, fmt.Errorf("--update requires npm packagePath to match its declared artifact source")
			}
		case "sdkman":
			if len(updateObject(options, "checksums")) != 0 {
				return doc, nil, fmt.Errorf("--update does not support SDKMAN explicit checksums; update the artifact and checksums manually")
			}
			d.version, d.download = options, options
		case "homebrew":
			manifest := updateObject(options, "manifest")
			d.variant += ":" + updateString(manifest, "type")
			if updateString(manifest, "installLayout") == "jvm-distribution" && updateString(manifest, "type") == "formula" {
				d.variant = "homebrew:jvm"
			} else if updateString(manifest, "installLayout") != "" {
				return doc, nil, fmt.Errorf("--update does not support this Homebrew install layout")
			}
			d.version, d.download = manifest, manifest
		case "winget":
			installer := updateObject(options, "installer")
			d.variant += ":" + updateString(installer, "type")
			if d.variant == "winget:zip" && updateString(installer, "archivePath") != updateString(doc.Release.Artifacts[d.artifact], "source") {
				return doc, nil, fmt.Errorf("--update requires WinGet archivePath to match its declared artifact source")
			}
			d.version, d.download = updateObject(options, "package"), installer
			if updateString(d.version, "releaseNotesUrl") != "" {
				return doc, nil, fmt.Errorf("--update does not support explicit WinGet releaseNotesUrl; update release-specific metadata manually")
			}
		}
		if _, err := normalizeInitDestinations([]string{d.variant}); err != nil {
			return doc, nil, fmt.Errorf("--update does not support destination layout %s", d.variant)
		}
		if provider != "npm" && (updateString(d.version, "version") == "" || updateString(d.download, "url") == "") {
			return doc, nil, fmt.Errorf("--update requires existing version and URL metadata")
		}
		used[d.artifact] = true
		destinations = append(destinations, d)
	}
	if len(used) != len(doc.Release.Artifacts) {
		return doc, nil, fmt.Errorf("--update does not support unreferenced artifacts")
	}
	return doc, destinations, nil
}

func updateIdentity(id, previous string, records []runstate.Record) error {
	if _, err := domain.NewReleaseID(id); err != nil {
		return fmt.Errorf("supply a valid new --release ID")
	}
	for _, record := range records {
		if record.ReleaseID == id {
			return fmt.Errorf("release %q already has saved plan %q; inspect distroplane runs --release or status --release for the existing run", id, record.PlanID)
		}
	}
	if id == previous {
		return fmt.Errorf("release ID is unchanged; use status or resume to continue the existing release")
	}
	return nil
}

func updateFields(raw []string) (map[string]string, error) {
	fields := map[string]string{}
	for _, flag := range raw {
		key, value, ok := strings.Cut(flag, "=")
		if !ok || key == "" || value == "" || fields[key] != "" {
			return nil, fmt.Errorf("use unique --set FIELD=VALUE inputs")
		}
		fields[key] = value
	}
	return fields, nil
}

func runInitUpdate(ctx context.Context, path, releaseID string, artifacts, rawFields []string, input io.Reader, prompt, jsonMode bool, stdout, stderr io.Writer) int {
	if path == "" {
		return writeCommandError(stderr, jsonMode, exitUsage, "usage", fmt.Errorf("--output is required"))
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return writeCommandError(stderr, jsonMode, exitInvalid, "invalid_output", err)
	}
	original, err := readUpdateConfig(path)
	if err != nil {
		return writeCommandError(stderr, jsonMode, exitInvalid, "invalid_config", err)
	}
	doc, destinations, err := decodeUpdate(original)
	if err != nil {
		return writeCommandError(stderr, jsonMode, exitInvalid, "invalid_template", err)
	}
	base := filepath.Dir(path)
	for dir := base; ; dir = filepath.Dir(dir) {
		if filepath.Base(dir) == ".distroplane" {
			return writeCommandError(stderr, jsonMode, exitInvalid, "unsafe_output", fmt.Errorf("configuration must be outside saved execution state"))
		}
		if dir == filepath.Dir(dir) {
			break
		}
	}
	store := runstate.Store{Root: filepath.Join(base, ".distroplane")}
	records, err := store.Inventory()
	if err != nil {
		return writeCommandError(stderr, jsonMode, exitInvalid, "catalog_invalid", err)
	}
	reader := bufio.NewReader(input)
	if prompt && ctx.Done() != nil {
		reader = bufio.NewReader(initContextReader{ctx: ctx, input: input})
	}
	if releaseID == "" && prompt {
		releaseID, err = promptValidated(reader, stderr, "new release ID (current "+safeDisplay(doc.Release.ID)+")", func(value string) error {
			return updateIdentity(value, doc.Release.ID, records)
		})
		if err != nil {
			return initFailure(ctx, stderr, jsonMode, exitInvalid, "missing_release", err)
		}
	}
	if err := updateIdentity(releaseID, doc.Release.ID, records); err != nil {
		return writeCommandError(stderr, jsonMode, exitInvalid, "release_conflict", err)
	}
	fields, err := updateFields(rawFields)
	if err != nil {
		return writeCommandError(stderr, jsonMode, exitUsage, "invalid_option", err)
	}
	// A scripted update must explicitly select every artifact, not discover a stale build.
	paths, err := updateArtifactPaths(doc, destinations, artifacts, base, prompt, reader, stderr)
	if err != nil {
		return initFailure(ctx, stderr, jsonMode, exitInvalid, "invalid_artifact", err)
	}
	selected := map[string]initArtifact{}
	changes := make([]updateChange, 0, len(destinations))
	sources := map[string]string{}
	builder := initBuilder{fields: fields, reader: reader, prompts: prompt, stderr: stderr}
	for _, d := range destinations {
		provider, _, _ := strings.Cut(d.variant, ":")
		artifact, source, err := inspectInitArtifact(base, paths[provider])
		if err != nil {
			return writeCommandError(stderr, jsonMode, exitInvalid, "invalid_artifact", err)
		}
		if err := validateInitArtifactKind(d.variant, source); err != nil {
			return writeCommandError(stderr, jsonMode, exitInvalid, "invalid_artifact", err)
		}
		old := updateString(doc.Release.Artifacts[d.artifact], "source")
		oldPath := old
		if !filepath.IsAbs(oldPath) {
			oldPath = filepath.Join(base, old)
		}
		if err := validateUpdateArtifactPath(base, doc, records, artifact.Path); err != nil {
			return writeCommandError(stderr, jsonMode, exitInvalid, "invalid_artifact", err)
		}
		if previous, exists := sources[d.artifact]; exists && previous != source {
			return writeCommandError(stderr, jsonMode, exitInvalid, "invalid_artifact", fmt.Errorf("destinations sharing artifact %q must select the same new path", d.artifact))
		}
		sources[d.artifact] = source
		selected[provider] = artifact
		change := updateChange{Destination: d.id + " (" + d.variant + ")", OldArtifact: old, NewArtifact: source, SHA256: artifact.SHA256}
		builder.destination = d.variant
		if provider == "npm" {
			if _, _, err := inspectInitArtifact(base, oldPath); err != nil {
				return writeCommandError(stderr, jsonMode, exitInvalid, "invalid_artifact", fmt.Errorf("original npm artifact is unavailable; retain it for recovery or edit manually"))
			}
			oldName, oldVersion, err := inspectUpdateNPM(oldPath)
			if err != nil {
				return writeCommandError(stderr, jsonMode, exitInvalid, "invalid_artifact", fmt.Errorf("original npm package metadata is unavailable; preserve the original package or edit manually: %w", err))
			}
			name, version, err := inspectUpdateNPM(artifact.Path)
			if err != nil {
				return writeCommandError(stderr, jsonMode, exitInvalid, "invalid_artifact", err)
			}
			if name != oldName || version == oldVersion {
				return writeCommandError(stderr, jsonMode, exitInvalid, "invalid_artifact", fmt.Errorf("npm update requires the same package name and a different package.json version"))
			}
			change.OldVersion, change.NewVersion = oldName+"@"+oldVersion, name+"@"+version
			d.options["packagePath"] = source
		} else {
			change.OldVersion = updateString(d.version, "version")
			change.OldURL = updateString(d.download, "url")
			version, err := updateRequired(&builder, provider+".version", change.OldVersion, true)
			if err != nil {
				return initFailure(ctx, stderr, jsonMode, exitInvalid, "missing_metadata", err)
			}
			url, err := updateRequired(&builder, provider+".url", change.OldURL, false)
			if err != nil {
				return initFailure(ctx, stderr, jsonMode, exitInvalid, "missing_metadata", err)
			}
			d.version["version"], d.download["url"] = version, url
			change.NewVersion, change.NewURL = version, url
			if d.variant == "winget:zip" {
				d.download["archivePath"] = source
			}
		}
		encoded, err := json.Marshal(d.entry)
		if err != nil {
			return writeCommandError(stderr, jsonMode, exitOperational, "encode_failed", err)
		}
		doc.Destinations[provider] = encoded
		changes = append(changes, change)
	}
	if len(fields) != 0 {
		return writeCommandError(stderr, jsonMode, exitUsage, "unknown_option", fmt.Errorf("--update accepts only destination version and URL changes"))
	}
	previousArtifacts := make(map[string]map[string]any, len(doc.Release.Artifacts))
	for name, source := range sources {
		previousArtifacts[name] = map[string]any{"source": updateString(doc.Release.Artifacts[name], "source")}
		doc.Release.Artifacts[name]["source"] = source
	}
	oldRelease := doc.Release.ID
	doc.Release.ID = releaseID
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return writeCommandError(stderr, jsonMode, exitOperational, "encode_failed", err)
	}
	if _, err := official.Decode(bytes.NewReader(data)); err != nil {
		return writeCommandError(stderr, jsonMode, exitInvalid, "invalid_template", err)
	}
	if !jsonMode {
		fmt.Fprintf(stderr, "Release: %s -> %s\nDestinations unchanged:\n", safeDisplay(oldRelease), safeDisplay(releaseID))
		for _, c := range changes {
			fmt.Fprintf(stderr, "  %s: %s -> %s (SHA-256 %s)\n", safeDisplay(c.Destination), safeDisplay(c.OldArtifact), safeDisplay(c.NewArtifact), c.SHA256)
			if c.NewVersion != "" {
				fmt.Fprintf(stderr, "    Version: %s -> %s\n", safeDisplay(c.OldVersion), safeDisplay(c.NewVersion))
			}
			if c.NewURL != "" {
				fmt.Fprintf(stderr, "    URL: %s -> %s\n", safeDisplay(c.OldURL), safeDisplay(c.NewURL))
			}
		}
	}
	if prompt {
		answer, err := promptInit(reader, stderr, "To save, type update "+safeDisplay(releaseID))
		if err != nil || answer != "update "+releaseID {
			return initFailure(ctx, stderr, jsonMode, exitInvalid, "approval_denied", fmt.Errorf("configuration update was not approved"))
		}
	}
	if err := ctx.Err(); err != nil {
		return writeCommandError(stderr, jsonMode, exitOperational, "cancelled", err)
	}
	// Recheck the catalog and artifact bytes after review, before replacing the file.
	records, err = store.Inventory()
	if err != nil {
		return writeCommandError(stderr, jsonMode, exitInvalid, "catalog_invalid", err)
	}
	if err := updateIdentity(releaseID, oldRelease, records); err != nil {
		return writeCommandError(stderr, jsonMode, exitInvalid, "release_conflict", err)
	}
	previous := updateDocument{Release: updateRelease{Artifacts: previousArtifacts}}
	for _, artifact := range selected {
		if err := validateUpdateArtifactPath(base, previous, records, artifact.Path); err != nil {
			return writeCommandError(stderr, jsonMode, exitInvalid, "invalid_artifact", err)
		}
		actual, _, err := inspectInitArtifact(base, artifact.Path)
		if err != nil || actual != artifact {
			return writeCommandError(stderr, jsonMode, exitInvalid, "invalid_artifact", fmt.Errorf("artifact changed during update review"))
		}
	}
	backup, err := writeUpdatedConfig(ctx, path, original, append(data, '\n'))
	if err != nil {
		return initFailure(ctx, stderr, jsonMode, exitOperational, "write_failed", err)
	}
	if jsonMode {
		return writeJSON(stdout, struct {
			OutputSchemaVersion string         `json:"outputSchemaVersion"`
			ConfigPath          string         `json:"configPath"`
			BackupPath          string         `json:"backupPath"`
			OldRelease          string         `json:"oldRelease"`
			Release             string         `json:"release"`
			Changes             []updateChange `json:"changes"`
		}{outputSchemaVersion, path, backup, oldRelease, releaseID, changes})
	}
	fmt.Fprintf(stdout, "configuration %s\nbackup %s\n", safeDisplay(path), safeDisplay(backup))
	writeInitFollowUp(stdout, path)
	return exitOK
}

func updateRequired(b *initBuilder, key, previous string, different bool) (string, error) {
	validate := func(value string) error {
		if err := validateInitField(key, value, b.destination, ""); err != nil {
			return err
		}
		if different && value == previous {
			return fmt.Errorf("%s must differ from the previous release", key)
		}
		return nil
	}
	if value, exists := b.fields[key]; exists {
		delete(b.fields, key)
		return value, validate(value)
	}
	if !b.prompts {
		return "", fmt.Errorf("supply --set %s=VALUE", key)
	}
	return promptValidated(b.reader, b.stderr, initFieldPrompt(key, b.destination)+" (previous "+safeDisplay(previous)+")", validate)
}

func sameUpdateArtifact(old, next string) bool {
	if filepath.Clean(old) == filepath.Clean(next) {
		return true
	}
	a, errA := os.Stat(old)
	b, errB := os.Stat(next)
	return errA == nil && errB == nil && os.SameFile(a, b)
}

func validateUpdateArtifactPath(base string, doc updateDocument, records []runstate.Record, next string) error {
	for _, artifact := range doc.Release.Artifacts {
		old := updateString(artifact, "source")
		if !filepath.IsAbs(old) {
			old = filepath.Join(base, old)
		}
		if sameUpdateArtifact(old, next) {
			return fmt.Errorf("choose a new artifact path; preserve original artifacts for existing runs")
		}
	}
	for _, record := range records {
		plan, err := planner.Load(record.PlanPath)
		if err != nil {
			return fmt.Errorf("cannot verify saved artifact paths: %w", err)
		}
		for _, artifact := range plan.Plan().Release().Artifacts() {
			if sameUpdateArtifact(artifact.Source(), next) {
				return fmt.Errorf("artifact path belongs to saved release %q; choose a new path", record.ReleaseID)
			}
		}
	}
	return nil
}

func updateArtifactPaths(doc updateDocument, destinations []updateDestination, raw []string, base string, prompt bool, reader *bufio.Reader, stderr io.Writer) (map[string]string, error) {
	providers := map[string]bool{}
	for _, d := range destinations {
		provider, _, _ := strings.Cut(d.variant, ":")
		providers[provider] = true
	}
	var shared string
	explicit := map[string]string{}
	for _, item := range raw {
		provider, path, scoped := strings.Cut(item, "=")
		if scoped && providers[provider] {
			if path == "" || explicit[provider] != "" {
				return nil, fmt.Errorf("duplicate or empty artifact mapping")
			}
			explicit[provider] = path
		} else {
			if shared != "" || scoped && !strings.ContainsAny(provider, "/\\") {
				return nil, fmt.Errorf("use a shared --artifact PATH or selected PROVIDER=PATH mappings")
			}
			shared = item
		}
	}
	byArtifact := map[string]string{}
	for _, d := range destinations {
		provider, _, _ := strings.Cut(d.variant, ":")
		value := explicit[provider]
		if value == "" {
			value = shared
		}
		if value == "" {
			continue
		}
		if !filepath.IsAbs(value) {
			value = filepath.Join(base, value)
		}
		value = filepath.Clean(value)
		if prior := byArtifact[d.artifact]; prior != "" && prior != value {
			return nil, fmt.Errorf("destinations sharing artifact %q must select the same new path", d.artifact)
		}
		byArtifact[d.artifact] = value
	}
	paths := map[string]string{}
	for _, d := range destinations {
		provider, _, _ := strings.Cut(d.variant, ":")
		value := byArtifact[d.artifact]
		if value == "" {
			if !prompt {
				return nil, fmt.Errorf("supply --artifact %s=PATH", provider)
			}
			compatible := func(value string) error {
				for _, target := range destinations {
					if target.artifact == d.artifact {
						if err := validateInitArtifactKind(target.variant, value); err != nil {
							return err
						}
					}
				}
				if !filepath.IsAbs(value) {
					value = filepath.Join(base, value)
				}
				info, err := os.Lstat(value)
				if err != nil || !info.Mode().IsRegular() || info.Size() == 0 || !initSafeDescendant(base, value) {
					return fmt.Errorf("choose an existing nonempty regular artifact")
				}
				return validateUpdateArtifactPath(base, doc, nil, value)
			}
			var candidates []string
			if found, err := discoverArtifacts(base); err == nil {
				for _, candidate := range found {
					if compatible(candidate) == nil {
						candidates = append(candidates, candidate)
						fmt.Fprintf(stderr, "  %d. %s\n", len(candidates), safeDisplay(candidate))
					}
				}
			}
			_, err := promptValidated(reader, stderr, "new artifact path or candidate number for "+safeDisplay(d.artifact), func(answer string) error {
				if index, err := strconv.Atoi(answer); err == nil {
					if index < 1 || index > len(candidates) {
						return fmt.Errorf("choose a listed candidate or enter a path")
					}
					answer = candidates[index-1]
				}
				if err := compatible(answer); err != nil {
					return err
				}
				value = answer
				return nil
			})
			if err != nil {
				return nil, err
			}
			byArtifact[d.artifact] = value
		}
		paths[provider] = value
	}
	return paths, nil
}

func writeUpdatedConfig(ctx context.Context, path string, original, data []byte) (string, error) {
	dir := filepath.Dir(path)
	if info, err := os.Lstat(dir); err != nil || !info.IsDir() {
		return "", fmt.Errorf("output parent must be an existing regular directory")
	}
	stage, err := os.CreateTemp(dir, ".distroplane-update-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(stage.Name())
	defer stage.Close()
	if _, err := stage.Write(data); err != nil {
		return "", err
	}
	if err := stage.Sync(); err != nil {
		return "", err
	}
	if err := stage.Close(); err != nil {
		return "", err
	}
	current, err := readUpdateConfig(path)
	if err != nil || !bytes.Equal(current, original) {
		return "", fmt.Errorf("configuration changed during update; review it again")
	}
	sum := sha256.Sum256(original)
	backup := path + ".bak." + hex.EncodeToString(sum[:])
	saved, err := os.CreateTemp(dir, ".distroplane-backup-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(saved.Name())
	defer saved.Close()
	if _, err := saved.Write(original); err != nil {
		return "", err
	}
	if err := saved.Sync(); err != nil {
		return "", err
	}
	if err := saved.Close(); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := os.Link(saved.Name(), backup); err != nil {
		return "", fmt.Errorf("cannot install non-overwriting update backup: %w", err)
	}
	current, err = readUpdateConfig(path)
	if err != nil || !bytes.Equal(current, original) {
		return backup, fmt.Errorf("configuration changed during update; original backup retained")
	}
	if err := ctx.Err(); err != nil {
		return backup, err
	}
	if err := os.Rename(stage.Name(), path); err != nil {
		return backup, fmt.Errorf("replace configuration; original backup retained: %w", err)
	}
	return backup, nil
}
