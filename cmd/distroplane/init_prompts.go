package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/aalsanie/distroplane/internal/canonicaljson"
	"github.com/aalsanie/distroplane/internal/config"
)

var errEmptyInitAnswer = errors.New("empty answer")

type initContextReader struct {
	ctx   context.Context
	input io.Reader
}

func (r initContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	type result struct {
		n   int
		err error
	}
	buffer := make([]byte, len(p))
	done := make(chan result, 1)
	go func() {
		n, err := r.input.Read(buffer)
		done <- result{n: n, err: err}
	}()
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	case read := <-done:
		if err := r.ctx.Err(); err != nil {
			return 0, err
		}
		return copy(p, buffer[:read.n]), read.err
	}
}

func initFailure(ctx context.Context, stderr io.Writer, jsonMode bool, code int, kind string, err error) int {
	if ctx.Err() != nil {
		return writeCommandError(stderr, jsonMode, exitOperational, "cancelled", ctx.Err())
	}
	return writeCommandError(stderr, jsonMode, code, kind, err)
}

func promptValidated(reader *bufio.Reader, stderr io.Writer, field string, validate func(string) error) (string, error) {
	for {
		value, err := promptInit(reader, stderr, field)
		if err != nil && !errors.Is(err, errEmptyInitAnswer) {
			return "", err
		}
		if err == nil {
			err = validate(value)
		}
		if err == nil {
			return value, nil
		}
		fmt.Fprintf(stderr, "Invalid answer: %s\n", safeDisplay(err.Error()))
	}
}

func initFieldPrompt(key, destination string) string {
	examples := map[string]string{
		"sdkman.candidate": "existing SDKMAN candidate, e.g. mytool",
		"sdkman.version": "destination version, e.g. 1.2.3",
		"sdkman.url": "public HTTPS download URL, e.g. https://example.org/tool.zip",
		"homebrew.repository": "your tap Git repository, e.g. https://github.com/owner/homebrew-tap.git",
		"homebrew.path": "manifest path in your tap, e.g. Formula/tool.rb",
		"homebrew.name": "Ruby formula class, e.g. MyTool",
		"homebrew.description": "short package description, e.g. Release CLI",
		"homebrew.homepage": "project HTTPS homepage, e.g. https://example.org/",
		"homebrew.url": "public HTTPS artifact URL, e.g. https://example.org/tool.tar.gz",
		"homebrew.version": "destination version, e.g. 1.2.3",
		"homebrew.binary": "installed executable name, e.g. mytool",
		"homebrew.java-version": "required Java runtime (8, 11, 17, 21 or 25)",
		"homebrew.install-kind": "cask installation type (app, pkg or binary)",
		"homebrew.install-source": "path in the archive, e.g. MyTool.app",
		"homebrew.pr-repository": "pull request destination, e.g. owner/homebrew-tap",
		"winget.repository": "your manifest fork Git repository, e.g. https://github.com/owner/winget-pkgs.git",
		"winget.identifier": "WinGet package identifier, e.g. Publisher.MyTool",
		"winget.version": "destination version, e.g. 1.2.3",
		"winget.publisher": "package publisher name",
		"winget.name": "display name of the package",
		"winget.license": "actual package license, e.g. Apache-2.0",
		"winget.description": "short package description",
		"winget.url": "public HTTPS installer URL, e.g. https://example.org/tool.msi",
		"winget.architecture": "installer architecture (x86, x64, arm, arm64 or neutral)",
		"winget.pr-repository": "pull request destination, e.g. microsoft/winget-pkgs",
		"winget.nested-type": "installer inside ZIP (portable, exe or msi)",
		"winget.nested-files": "relative installer path, e.g. bin/tool.exe; use --set for advanced JSON arrays",
	}
	if destination == "homebrew:cask" {
		examples["homebrew.name"] = "cask token, e.g. my-tool"
		examples["homebrew.path"] = "manifest path in your tap, e.g. Casks/my-tool.rb"
	}
	if hint := examples[key]; hint != "" {
		return key + " (" + hint + ")"
	}
	return key
}

var (
	initFormulaName = regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`)
	initCaskName = regexp.MustCompile(`^[a-z0-9][a-z0-9+._-]*$`)
	initWingetID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9+_-]*(\.[A-Za-z0-9][A-Za-z0-9+_-]*)+$`)
	initLauncher = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
)

func validateInitField(key, value, destination, nestedType string) error {
	if err := config.ValidateText(key, value, false); err != nil {
		return err
	}
	invalid := func() error { return fmt.Errorf("%s: %s", key, initFieldPrompt(key, destination)) }
	switch key {
	case "sdkman.url", "homebrew.url", "homebrew.homepage", "winget.url":
		u, err := url.Parse(value)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" {
			return fmt.Errorf("%s: enter a public HTTPS URL without credentials, query or fragment", key)
		}
	case "homebrew.repository", "winget.repository":
		u, err := url.Parse(value)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.Trim(u.Path, "/") == "" {
			return fmt.Errorf("%s: enter the repository HTTPS URL without credentials", key)
		}
	case "homebrew.pr-repository", "winget.pr-repository":
		owner, name, ok := strings.Cut(value, "/")
		if !ok || owner == "" || name == "" || strings.ContainsAny(value, " \\:@?#") || strings.Contains(name, "/") {
			return invalid()
		}
	case "homebrew.path":
		if !validInitRelativePath(value) || !strings.HasSuffix(value, ".rb") {
			return invalid()
		}
	case "homebrew.name":
		pattern := initFormulaName
		if destination == "homebrew:cask" {
			pattern = initCaskName
		}
		if !pattern.MatchString(value) {
			return invalid()
		}
	case "homebrew.binary":
		if strings.ContainsAny(value, "/\\") || value == "." || value == ".." || (destination == "homebrew:jvm" && !initLauncher.MatchString(value)) {
			return invalid()
		}
	case "homebrew.java-version":
		if !slices.Contains([]string{"8", "11", "17", "21", "25"}, value) {
			return invalid()
		}
	case "homebrew.install-kind":
		if !slices.Contains([]string{"app", "pkg", "binary"}, value) {
			return invalid()
		}
	case "homebrew.install-source":
		if !validInitRelativePath(value) {
			return invalid()
		}
	case "sdkman.candidate", "sdkman.version":
		limit := 256
		if key == "sdkman.candidate" {
			limit = 128
		}
		if len(value) > limit || strings.ContainsAny(value, " /\\") {
			return invalid()
		}
	case "winget.identifier":
		if !initWingetID.MatchString(value) {
			return invalid()
		}
	case "winget.version":
		if len(value) > 128 || strings.ContainsAny(value, "/\\") {
			return invalid()
		}
	case "winget.publisher", "winget.name", "winget.license", "winget.description":
		if len(value) > 512 {
			return invalid()
		}
	case "winget.architecture":
		if !slices.Contains([]string{"x86", "x64", "arm", "arm64", "neutral"}, value) {
			return invalid()
		}
	case "winget.nested-type":
		if !slices.Contains([]string{"portable", "exe", "msi"}, value) {
			return invalid()
		}
	case "winget.nested-files":
		_, err := parseInitNestedFiles(value, nestedType)
		return err
	}
	return nil
}

func validInitRelativePath(value string) bool {
	return value != "." && !strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "../") &&
		value != ".." && !strings.ContainsAny(value, "\\:") && path.Clean(value) == value
}

func parseInitNestedFiles(value, kind string) ([]map[string]string, error) {
	invalid := fmt.Errorf("winget.nested-files: enter a relative installer path or a JSON array of relativeFilePath objects")
	var files []map[string]string
	if strings.HasPrefix(value, "[") {
		data, err := canonicaljson.Normalize([]byte(value))
		if err != nil || json.Unmarshal(data, &files) != nil {
			return nil, invalid
		}
	} else {
		files = []map[string]string{{"relativeFilePath": value}}
	}
	if len(files) == 0 || len(files) > 16 || (kind != "portable" && len(files) != 1) {
		return nil, invalid
	}
	extension := "." + kind
	if kind == "portable" {
		extension = ".exe"
	}
	paths, aliases := map[string]bool{}, map[string]bool{}
	for _, file := range files {
		name := file["relativeFilePath"]
		key := strings.ToLower(name)
		if !validInitRelativePath(name) || strings.ToLower(path.Ext(name)) != extension || paths[key] {
			return nil, invalid
		}
		paths[key] = true
		for field, value := range file {
			if field == "relativeFilePath" {
				continue
			}
			alias := strings.ToLower(value)
			if field != "portableCommandAlias" || kind != "portable" || !initLauncher.MatchString(value) || aliases[alias] {
				return nil, invalid
			}
			aliases[alias] = true
		}
	}
	return files, nil
}

func initSafeDescendant(base, target string) bool {
	relative, err := filepath.Rel(base, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return true
	}
	current := base
	for _, name := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, name)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return true
		}
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return false
		}
	}
	return true
}
