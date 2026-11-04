// Controlled test executable: no network access and no publishing. It validates
// the Maven adapter's subprocess, arguments and state handoff contract.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const schema = "1"

type state struct {
	PlanID    string `json:"planId"`
	RunID     string `json:"runId"`
	Attempts  int    `json:"attempts"`
	Completed bool   `json:"completed"`
}

func emit(value any) {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(data))
}

func fail(code, diagnostic string, exit int) {
	data, _ := json.Marshal(map[string]any{
		"outputSchemaVersion": schema,
		"error":               map[string]any{"kind": "fixture_failure", "code": code, "message": "controlled fixture failure"},
	})
	fmt.Fprintln(os.Stderr, string(data))
	os.Exit(exit)
}

func main() {
	if len(os.Args) < 2 {
		fail("INVALID_ARGUMENT", "", 2)
	}
	command := os.Args[1]
	if command == "version" {
		emit(map[string]any{"outputSchemaVersion": schema, "version": "0.9.3"})
		return
	}
	for _, arg := range os.Args[2:] {
		if strings.Contains(arg, "SECRET-CANARY") {
			fail("INVALID_ARGUMENT", "", 3)
		}
	}
	dir, err := os.Getwd()
	if err != nil {
		fail("INVALID_CONFIG", "", 3)
	}
	stateDir := filepath.Join(dir, ".distroplane")
	stateFile := filepath.Join(stateDir, "fixture-run.json")
	read := func() (state, bool) {
		var s state
		b, e := os.ReadFile(stateFile)
		if e != nil {
			return s, false
		}
		if json.Unmarshal(b, &s) != nil {
			fail("STATE_CORRUPT", "", 3)
		}
		return s, true
	}
	write := func(s state) {
		if os.MkdirAll(stateDir, 0700) != nil {
			fail("STATE_CORRUPT", "", 3)
		}
		b, _ := json.Marshal(s)
		if os.WriteFile(stateFile, b, 0600) != nil {
			fail("STATE_CORRUPT", "", 3)
		}
	}
	plan := func() any {
		return map[string]any{
			"outputSchemaVersion": schema, "planId": "fixture-plan",
			"path": filepath.Join(dir, "fixture-plan.json"), "targets": 1, "operations": 1,
		}
	}
	run := func(s state) any {
		result := "PUBLISHED"
		if !s.Completed {
			result = "WAITING_EXTERNAL"
		}
		return map[string]any{
			"outputSchemaVersion": schema, "planId": s.PlanID, "runId": s.RunID,
			"completed": s.Completed, "cancelled": false,
			"targets": []any{map[string]any{
				"id": "fixture", "state": result, "reconcileRequired": !s.Completed, "ambiguous": false,
			}},
			"operations": []any{map[string]any{
				"id": "fixture-operation", "targetId": "fixture", "state": result,
				"attempt": s.Attempts, "reconcileRequired": !s.Completed, "ambiguous": false, "retryable": false,
			}},
		}
	}
	output := func(s state, publication bool) {
		if publication {
			emit(map[string]any{
				"outputSchemaVersion": schema, "plan": plan(), "approved": true,
				"journalPath": stateFile, "run": run(s),
			})
		} else {
			emit(run(s))
		}
	}
	has := func(flag string) bool {
		for _, a := range os.Args[2:] {
			if a == flag {
				return true
			}
		}
		return false
	}
	switch command {
	case "init":
		emit(map[string]any{
			"outputSchemaVersion": schema, "configPath": filepath.Join(dir, "distroplane.json"),
			"artifact":  map[string]any{"path": "", "sha256": "", "size": 0},
			"artifacts": map[string]any{}, "destinations": []string{"npm"},
		})
	case "doctor":
		checks := []any{map[string]any{
			"code": "CONFIG_VALID", "severity": "PASS",
			"subject": "config", "message": "controlled fixture configuration",
		}}
		unavailable := false
		for i := 2; i < len(os.Args)-1; i++ {
			if os.Args[i] != "--credential" {
				continue
			}
			_, source, valid := strings.Cut(os.Args[i+1], "=")
			if !valid {
				fail("INVALID_CREDENTIAL_MAPPING", "", 2)
			}
			if os.Getenv(source) == "" {
				unavailable = true
				checks = append(checks, map[string]any{
					"code": "MISSING_CREDENTIAL", "severity": "FAIL",
					"subject": "fixture", "message": "mapped environment source unset",
				})
			}
		}
		if has("--remote") {
			checks = append(checks, map[string]any{
				"code": "REMOTE_REACHABLE", "severity": "PASS",
				"subject": "network", "message": "controlled local test probe",
			})
		}
		emit(map[string]any{"outputSchemaVersion": schema, "healthy": !unavailable, "checks": checks})
		if unavailable {
			os.Exit(3)
		}
	case "validate":
		emit(map[string]any{"outputSchemaVersion": schema, "valid": true, "schemaVersion": "2", "targets": 1})
	case "plan":
		if _, ok := read(); ok {
			fail("RUN_ALREADY_EXISTS", "", 3)
		}
		emit(plan())
	case "publish":
		if !has("--yes") && !has("--confirm-plan") {
			fail("APPROVAL_REQUIRED", "", 5)
		}
		if _, ok := read(); ok {
			fail("RUN_ALREADY_EXISTS", "", 3)
		}
		s := state{PlanID: "fixture-plan", RunID: "fixture-run", Attempts: 1}
		write(s)
		if _, err := os.Stat(filepath.Join(dir, ".fixture-hang")); err == nil {
			time.Sleep(30 * time.Second)
			return
		}
		if _, err := os.Stat(filepath.Join(dir, ".fixture-pending")); err == nil {
			output(s, true)
			os.Exit(4)
		}
		s.Completed = true
		write(s)
		output(s, true)
	case "resume":
		s, ok := read()
		if !ok {
			fail("STATE_CORRUPT", "", 3)
		}
		if _, err := os.Stat(filepath.Join(dir, ".fixture-pending")); err == nil {
			output(s, false)
			os.Exit(4)
		}
		s.Completed = true
		write(s)
		output(s, false)
	case "status":
		s, ok := read()
		if !ok {
			fail("STATE_CORRUPT", "", 3)
		}
		output(s, false)
		if !s.Completed {
			os.Exit(4)
		}
	default:
		fail("INVALID_ARGUMENT", "", 2)
	}
}
