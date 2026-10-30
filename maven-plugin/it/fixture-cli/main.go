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

type state struct {
	PlanID    string `json:"planId"`
	RunID     string `json:"runId"`
	Attempts  int    `json:"attempts"`
	Completed bool   `json:"completed"`
}

func fail(msg string, code int) { fmt.Fprintln(os.Stderr, msg); os.Exit(code) }
func main() {
	if len(os.Args) < 2 {
		fail("command required", 2)
	}
	command := os.Args[1]
	if command == "version" {
		fmt.Println(`{"version":"0.9.2"}`)
		return
	}
	for _, arg := range os.Args[2:] {
		if strings.Contains(arg, "SECRET-CANARY") {
			fail("secret value found in command line", 3)
		}
	}
	dir, err := os.Getwd()
	if err != nil {
		fail("workspace missing", 3)
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
			fail("corrupt run", 3)
		}
		return s, true
	}
	write := func(s state) {
		if os.MkdirAll(stateDir, 0700) != nil {
			fail("state directory failed", 3)
		}
		b, _ := json.Marshal(s)
		if os.WriteFile(stateFile, b, 0600) != nil {
			fail("state write failed", 3)
		}
	}
	output := func(s state) {
		b, _ := json.Marshal(map[string]any{"plan": map[string]any{"planId": s.PlanID}, "run": map[string]any{"runId": s.RunID, "completed": s.Completed}})
		fmt.Println(string(b))
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
	case "validate":
		fmt.Println(`{"valid":true}`)
	case "plan":
		if _, ok := read(); ok {
			fail("existing durable run", 3)
		}
		fmt.Println(`{"planId":"fixture-plan"}`)
	case "publish":
		if !has("--yes") && !has("--confirm-plan") {
			fail("approval required", 5)
		}
		if _, ok := read(); ok {
			fail("publication already claimed: use resume", 6)
		}
		s := state{PlanID: "fixture-plan", RunID: "fixture-run", Attempts: 1}
		write(s)
		if _, err := os.Stat(filepath.Join(dir, ".fixture-hang")); err == nil {
			time.Sleep(30 * time.Second)
			return
		}
		if _, err := os.Stat(filepath.Join(dir, ".fixture-pending")); err == nil {
			output(s)
			os.Exit(4)
		}
		s.Completed = true
		write(s)
		output(s)
	case "resume":
		s, ok := read()
		if !ok {
			fail("original run missing; cannot resume", 3)
		}
		if _, err := os.Stat(filepath.Join(dir, ".fixture-pending")); err == nil {
			output(s)
			os.Exit(4)
		}
		s.Completed = true
		write(s)
		output(s)
	case "status":
		s, ok := read()
		if !ok {
			fail("run not found", 3)
		}
		output(s)
	default:
		fail("unsupported controlled fixture goal", 2)
	}
}
