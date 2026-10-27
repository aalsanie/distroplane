package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aalsanie/distroplane/internal/config"
	"github.com/aalsanie/distroplane/internal/domain"
)

const maxDiscoveryEntries = 256
const maxArtifactCandidates = 32
const candidateMaxAge = 30 * 24 * time.Hour

type repeatedFlag []string

func (v *repeatedFlag) String() string { return "" }
func (v *repeatedFlag) Set(s string) error {
	if strings.TrimSpace(s) == "" {
		return fmt.Errorf("empty value")
	}
	*v = append(*v, s)
	return nil
}

type initArtifact struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type initOutput struct {
	OutputSchemaVersion string       `json:"outputSchemaVersion"`
	ConfigPath          string       `json:"configPath"`
	BackupPath          string       `json:"backupPath,omitempty"`
	Artifact            initArtifact `json:"artifact"`
	Destinations        []string     `json:"destinations"`
}

// runInitContext never starts a provider or performs a network request. Only a
// real terminal session can enter interactive mode; automation must supply data.
func runInitContext(ctx context.Context, args []string, input io.Reader, interactive bool, stdout, stderr io.Writer) int {
	var releaseID, artifactPath, outputPath string
	var force, jsonMode, noninteractive bool
	var destinations, options repeatedFlag
	fs := commandFlags("init")
	fs.StringVar(&releaseID, "release", "", "")
	fs.StringVar(&artifactPath, "artifact", "", "")
	fs.StringVar(&outputPath, "output", "distroplane.json", "")
	fs.BoolVar(&force, "force", false, "")
	fs.BoolVar(&jsonMode, "json", false, "")
	fs.BoolVar(&noninteractive, "non-interactive", false, "")
	fs.Var(&destinations, "destination", "")
	fs.Var(&options, "set", "")
	if err := parseCommand(fs, args); err != nil {
		return writeCommandError(stderr, hasJSON(args), exitUsage, "usage", err)
	}
	if err := ctx.Err(); err != nil {
		return writeCommandError(stderr, jsonMode, exitOperational, "cancelled", err)
	}
	if outputPath == "" {
		return writeCommandError(stderr, jsonMode, exitUsage, "usage", fmt.Errorf("--output is required"))
	}
	outputPath, err := filepath.Abs(outputPath)
	if err != nil {
		return writeCommandError(stderr, jsonMode, exitInvalid, "invalid_output", err)
	}
	baseDir := filepath.Dir(outputPath)
	info, err := os.Lstat(outputPath)
	if err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return writeCommandError(stderr, jsonMode, exitInvalid, "unsafe_output", fmt.Errorf("output is not a regular file"))
		}
		if !force {
			return writeCommandError(stderr, jsonMode, exitInvalid, "config_exists", fmt.Errorf("configuration exists; use --force to create a backup and replace it"))
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return writeCommandError(stderr, jsonMode, exitInvalid, "invalid_output", err)
	}
	allowPrompt := interactive && !noninteractive && !jsonMode && input != nil
	reader := bufio.NewReader(input)
	if len(destinations) == 0 && allowPrompt {
		value, err := promptInit(reader, stderr, "destinations (comma-separated: npm, sdkman, homebrew:formula, homebrew:cask, homebrew:jvm, winget:msi, winget:zip)")
		if err != nil {
			return writeCommandError(stderr, jsonMode, exitUsage, "missing_destinations", err)
		}
		destinations = append(destinations, strings.Split(value, ",")...)
	}
	destinations, err = normalizeInitDestinations(destinations)
	if err != nil {
		return writeCommandError(stderr, jsonMode, exitUsage, "invalid_destination", err)
	}
	if releaseID == "" && allowPrompt {
		releaseID, err = promptInit(reader, stderr, "release ID")
		if err != nil {
			return writeCommandError(stderr, jsonMode, exitUsage, "missing_release", err)
		}
	}
	if _, err := domain.NewReleaseID(releaseID); err != nil {
		return writeCommandError(stderr, jsonMode, exitUsage, "missing_release", fmt.Errorf("--release: %w", err))
	}
	fields := make(map[string]string)
	for _, option := range options {
		k, v, ok := strings.Cut(option, "=")
		if !ok || k == "" || v == "" {
			return writeCommandError(stderr, jsonMode, exitUsage, "invalid_option", fmt.Errorf("--set must be FIELD=VALUE"))
		}
		if _, exists := fields[k]; exists {
			return writeCommandError(stderr, jsonMode, exitUsage, "invalid_option", fmt.Errorf("duplicate --set field %q", k))
		}
		fields[k] = v
	}
	if artifactPath == "" {
		candidates, err := discoverArtifacts(baseDir)
		if err != nil {
			return writeCommandError(stderr, jsonMode, exitInvalid, "artifact_discovery_failed", err)
		}
		switch len(candidates) {
		case 0:
			if !allowPrompt {
				return writeCommandError(stderr, jsonMode, exitInvalid, "missing_artifact", fmt.Errorf("no recent build artifacts in target/, build/, or dist/; specify --artifact"))
			}
		case 1:
			artifactPath = candidates[0]
		default:
			if !allowPrompt {
				return writeCommandError(stderr, jsonMode, exitInvalid, "ambiguous_artifact", fmt.Errorf("%d build artifacts found; choose one with --artifact (candidates: %s)", len(candidates), strings.Join(candidates, ", ")))
			}
			fmt.Fprintf(stderr, "Artifact candidates: %s\n", strings.Join(candidates, ", "))
		}
		if artifactPath == "" && allowPrompt {
			artifactPath, err = promptInit(reader, stderr, "artifact path")
			if err != nil {
				return writeCommandError(stderr, jsonMode, exitUsage, "missing_artifact", err)
			}
		}
	}
	artifact, source, err := inspectInitArtifact(baseDir, artifactPath)
	if err != nil {
		return writeCommandError(stderr, jsonMode, exitInvalid, "invalid_artifact", err)
	}
	if !jsonMode {
		fmt.Fprintf(stderr, "Artifact %s\nSHA-256 %s\nSize %d bytes\n", artifact.Path, artifact.SHA256, artifact.Size)
	}
	b := initBuilder{fields: fields, reader: reader, prompts: allowPrompt, stderr: stderr}
	dest := make(map[string]any)
	for _, variant := range destinations {
		provider, _, _ := strings.Cut(variant, ":")
		options, err := b.providerOptions(variant, source)
		if err != nil {
			return writeCommandError(stderr, jsonMode, exitInvalid, "missing_metadata", err)
		}
		dest[provider] = map[string]any{"artifact": "release", "options": options}
	}
	if len(b.fields) != 0 {
		keys := make([]string, 0, len(b.fields))
		for key := range b.fields {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		return writeCommandError(stderr, jsonMode, exitUsage, "unknown_option", fmt.Errorf("unknown --set field %q", keys[0]))
	}
	document := map[string]any{
		"schemaVersion": "2",
		"release":       map[string]any{"id": releaseID, "artifacts": map[string]any{"release": map[string]any{"source": source}}},
		"destinations":  dest,
	}
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return writeCommandError(stderr, jsonMode, exitOperational, "encode_failed", err)
	}
	if _, err := config.Decode(strings.NewReader(string(data))); err != nil {
		return writeCommandError(stderr, jsonMode, exitInvalid, "invalid_template", err)
	}
	if err := ctx.Err(); err != nil {
		return writeCommandError(stderr, jsonMode, exitOperational, "cancelled", err)
	}
	backup, err := writeInitConfig(outputPath, append(data, '\n'), force)
	if err != nil {
		return writeCommandError(stderr, jsonMode, exitOperational, "write_failed", err)
	}
	result := initOutput{OutputSchemaVersion: outputSchemaVersion, ConfigPath: outputPath, BackupPath: backup, Artifact: artifact, Destinations: destinations}
	if jsonMode {
		return writeJSON(stdout, result)
	}
	fmt.Fprintf(stdout, "configuration %s\n", outputPath)
	if backup != "" {
		fmt.Fprintf(stdout, "backup %s\n", backup)
	}
	fmt.Fprintln(stdout, "review generated metadata, then run 'distroplane plan --config' before publishing")
	return exitOK
}

func promptInit(reader *bufio.Reader, stderr io.Writer, field string) (string, error) {
	fmt.Fprintf(stderr, "%s: ", field)
	value, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s is required", field)
	}
	return value, nil
}

func normalizeInitDestinations(raw []string) ([]string, error) {
	seen := make(map[string]bool)
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		for _, name := range strings.Split(item, ",") {
			name = strings.TrimSpace(name)
			if name == "homebrew" {
				name = "homebrew:formula"
			}
			if name == "winget" {
				name = "winget:msi"
			}
			switch name {
			case "npm", "sdkman", "homebrew:formula", "homebrew:cask", "homebrew:jvm", "winget:msi", "winget:zip":
			default:
				return nil, fmt.Errorf("unsupported destination %q", name)
			}
			p, _, _ := strings.Cut(name, ":")
			if seen[p] {
				return nil, fmt.Errorf("duplicate destination %q; choose one layout per provider", p)
			}
			seen[p] = true
			out = append(out, name)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("at least one --destination is required")
	}
	sort.Strings(out)
	return out, nil
}

func discoverArtifacts(baseDir string) ([]string, error) {
	var found []string
	for _, name := range []string{"target", "build/distributions", "build/libs", "build", "dist"} {
		dir := filepath.Join(baseDir, filepath.FromSlash(name))
		info, err := os.Lstat(dir)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		files, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		if len(files) > maxDiscoveryEntries {
			return nil, fmt.Errorf("artifact directory %q has too many entries; provide --artifact", name)
		}
		for _, file := range files {
			if file.IsDir() || file.Type()&os.ModeSymlink != 0 {
				continue
			}
			if !candidateExtension(file.Name()) {
				continue
			}
			meta, err := os.Lstat(filepath.Join(dir, file.Name()))
			if err != nil {
				return nil, err
			}
			if !meta.Mode().IsRegular() || meta.Size() == 0 || meta.ModTime().Before(time.Now().Add(-candidateMaxAge)) || meta.ModTime().After(time.Now().Add(10*time.Minute)) {
				continue
			}
			found = append(found, filepath.ToSlash(filepath.Join(name, file.Name())))
			if len(found) > maxArtifactCandidates {
				return nil, fmt.Errorf("too many artifact candidates; provide --artifact")
			}
		}
	}
	sort.Strings(found)
	return found, nil
}

func candidateExtension(name string) bool {
	name = strings.ToLower(name)
	for _, suffix := range []string{".tgz", ".tar.gz", ".zip", ".msi", ".exe", ".jar"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

func inspectInitArtifact(baseDir, chosen string) (initArtifact, string, error) {
	if chosen == "" {
		return initArtifact{}, "", fmt.Errorf("specify --artifact")
	}
	path := chosen
	if !filepath.IsAbs(path) {
		path = filepath.Join(baseDir, chosen)
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return initArtifact{}, "", err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return initArtifact{}, "", fmt.Errorf("artifact %q: %w", chosen, err)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return initArtifact{}, "", fmt.Errorf("artifact %q must be a nonempty regular file (not a symlink)", chosen)
	}
	file, err := os.Open(path)
	if err != nil {
		return initArtifact{}, "", err
	}
	defer file.Close()
	hash := sha256.New()
	n, err := io.Copy(hash, file)
	if err != nil {
		return initArtifact{}, "", err
	}
	if n != info.Size() {
		return initArtifact{}, "", fmt.Errorf("artifact changed during hashing")
	}
	source := path
	if relative, err := filepath.Rel(baseDir, path); err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		source = filepath.ToSlash(relative)
	}
	return initArtifact{Path: path, SHA256: hex.EncodeToString(hash.Sum(nil)), Size: n}, source, nil
}

type initBuilder struct {
	fields  map[string]string
	reader  *bufio.Reader
	prompts bool
	stderr  io.Writer
}

func (b *initBuilder) field(key, fallback string, required bool) (string, error) {
	if value, ok := b.fields[key]; ok {
		delete(b.fields, key)
		if value == "" {
			return "", fmt.Errorf("%s cannot be empty", key)
		}
		return value, nil
	}
	if fallback != "" {
		return fallback, nil
	}
	if b.prompts && required {
		value, err := promptInit(b.reader, b.stderr, key)
		if err != nil {
			return "", err
		}
		return value, nil
	}
	if required {
		return "", fmt.Errorf("%s is required; supply --set %s=VALUE", key, key)
	}
	return "", nil
}

func (b *initBuilder) required(key string) (string, error) { return b.field(key, "", true) }
func (b *initBuilder) optional(key string) string {
	value, _ := b.field(key, "", false)
	return value
}
func (b *initBuilder) defaulted(key, def string) string {
	value, _ := b.field(key, def, false)
	return value
}

func (b *initBuilder) providerOptions(destination, artifact string) (map[string]any, error) {
	switch destination {
	case "npm":
		mode := b.defaulted("npm.auth-mode", "token")
		return map[string]any{"packagePath": artifact, "registry": b.defaulted("npm.registry", "https://registry.npmjs.org/"),
			"authentication": map[string]any{"mode": mode, "credential": "npm-publish"}}, nil
	case "sdkman":
		candidate, err := b.required("sdkman.candidate")
		if err != nil {
			return nil, err
		}
		version, err := b.required("sdkman.version")
		if err != nil {
			return nil, err
		}
		url, err := b.required("sdkman.url")
		if err != nil {
			return nil, err
		}
		mode := b.defaulted("sdkman.auth-mode", "state")
		auth := map[string]any{"mode": mode}
		if mode == "state" {
			auth["emailCredential"] = "sdkman-vendor-email"
			auth["passwordCredential"] = "sdkman-vendor-password"
		} else if mode == "legacy" {
			auth["consumerKeyCredential"] = "sdkman-consumer-key"
			auth["consumerTokenCredential"] = "sdkman-consumer-token"
		} else {
			return nil, fmt.Errorf("sdkman.auth-mode: expected state or legacy")
		}
		options := map[string]any{"candidate": candidate, "version": version, "url": url, "authentication": auth}
		if value := b.optional("sdkman.platform"); value != "" {
			options["platform"] = value
		}
		return options, nil
	case "homebrew:formula", "homebrew:cask", "homebrew:jvm":
		repository, err := b.required("homebrew.repository")
		if err != nil {
			return nil, err
		}
		path, err := b.required("homebrew.path")
		if err != nil {
			return nil, err
		}
		name, err := b.required("homebrew.name")
		if err != nil {
			return nil, err
		}
		description, err := b.required("homebrew.description")
		if err != nil {
			return nil, err
		}
		homepage, err := b.required("homebrew.homepage")
		if err != nil {
			return nil, err
		}
		url, err := b.required("homebrew.url")
		if err != nil {
			return nil, err
		}
		version, err := b.required("homebrew.version")
		if err != nil {
			return nil, err
		}
		mode := b.defaulted("homebrew.mode", "direct")
		manifestType := "formula"
		if destination == "homebrew:cask" {
			manifestType = "cask"
		}
		manifest := map[string]any{"type": manifestType, "name": name, "description": description, "homepage": homepage, "url": url, "version": version}
		if manifestType == "formula" {
			binary, err := b.required("homebrew.binary")
			if err != nil {
				return nil, err
			}
			manifest["binary"] = binary
			if destination == "homebrew:jvm" {
				java, err := b.required("homebrew.java-version")
				if err != nil {
					return nil, err
				}
				manifest["installLayout"] = "jvm-distribution"
				manifest["javaVersion"] = java
			}
		} else {
			kind, err := b.required("homebrew.install-kind")
			if err != nil {
				return nil, err
			}
			src, err := b.required("homebrew.install-source")
			if err != nil {
				return nil, err
			}
			manifest["installKind"] = kind
			manifest["installSource"] = src
		}
		opts := map[string]any{"repository": repository, "path": path, "mode": mode,
			"manifest": manifest, "authentication": map[string]any{"credential": "tap-publish"}}
		if mode == "pull-request" {
			pr, err := b.required("homebrew.pr-repository")
			if err != nil {
				return nil, err
			}
			opts["pullRequest"] = map[string]any{"repository": pr}
		}
		return opts, nil
	case "winget:msi", "winget:zip":
		repo, err := b.required("winget.repository")
		if err != nil {
			return nil, err
		}
		identifier, err := b.required("winget.identifier")
		if err != nil {
			return nil, err
		}
		version, err := b.required("winget.version")
		if err != nil {
			return nil, err
		}
		publisher, err := b.required("winget.publisher")
		if err != nil {
			return nil, err
		}
		name, err := b.required("winget.name")
		if err != nil {
			return nil, err
		}
		license, err := b.required("winget.license")
		if err != nil {
			return nil, err
		}
		description, err := b.required("winget.description")
		if err != nil {
			return nil, err
		}
		url, err := b.required("winget.url")
		if err != nil {
			return nil, err
		}
		arch, err := b.required("winget.architecture")
		if err != nil {
			return nil, err
		}
		pr, err := b.required("winget.pr-repository")
		if err != nil {
			return nil, err
		}
		kind := "msi"
		installer := map[string]any{"architecture": arch, "type": kind, "url": url}
		if destination == "winget:zip" {
			kind = "zip"
			installer["type"] = kind
			installer["archivePath"] = artifact
			nestedType, err := b.required("winget.nested-type")
			if err != nil {
				return nil, err
			}
			nested, err := b.required("winget.nested-files")
			if err != nil {
				return nil, err
			}
			var files []map[string]string
			if err := json.Unmarshal([]byte(nested), &files); err != nil || len(files) == 0 {
				return nil, fmt.Errorf("winget.nested-files: expected nonempty JSON array of relativeFilePath objects")
			}
			installer["nestedInstallerType"] = nestedType
			installer["nestedInstallerFiles"] = files
		}
		return map[string]any{"repository": repo,
			"authentication": map[string]any{"credential": "winget-publish"},
			"package":        map[string]any{"identifier": identifier, "version": version, "publisher": publisher, "name": name, "license": license, "shortDescription": description},
			"installer":      installer, "pullRequest": map[string]any{"repository": pr}}, nil
	}
	return nil, fmt.Errorf("unsupported destination %q", destination)
}

func writeInitConfig(path string, data []byte, force bool) (string, error) {
	directory := filepath.Dir(path)
	if info, err := os.Lstat(directory); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("output parent must be an existing regular directory")
	}
	file, err := os.CreateTemp(directory, ".distroplane-init-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(0600); err != nil {
		file.Close()
		return "", err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return "", err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	if !force {
		if err := os.Link(file.Name(), path); err != nil {
			return "", fmt.Errorf("install config without overwrite: %w", err)
		}
		return "", nil
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Link(file.Name(), path); err != nil {
			return "", err
		}
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("refusing to overwrite non-regular config")
	}
	backup := path + ".bak"
	bak, err := os.OpenFile(backup, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", fmt.Errorf("backup must not overwrite existing file: %w", err)
	}
	defer func() { bak.Close() }()
	previous, err := os.Open(path)
	if err != nil {
		os.Remove(backup)
		return "", err
	}
	_, err = io.Copy(bak, previous)
	closeErr := previous.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = bak.Sync()
	}
	if err == nil {
		err = bak.Close()
	}
	if err != nil {
		os.Remove(backup)
		return "", err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return backup, err
	}
	return backup, nil
}
