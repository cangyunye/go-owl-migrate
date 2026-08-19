package cdc

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRenderRunnerInvocation(t *testing.T) {
	r := RunnerTemplate{
		Command:      "sqlplus",
		ArgsTemplate: "scott/tiger@host/svc -f {file}",
		Pending:      "./pending/",
		Done:         "./done/",
		Failed:       "./failed/",
	}
	script, err := r.RenderRunner()
	if err != nil {
		t.Fatalf("RenderRunner: %v", err)
	}
	for _, frag := range []string{
		"#!/bin/sh",
		`PENDING=./pending/`,
		`DONE=./done/`,
		`FAILED=./failed/`,
		`sqlplus scott/tiger@host/svc -f "$f"`,
		`mv "$f" "$DONE/$name"`,
		`mv "$f" "$FAILED/$name"`,
	} {
		if !strings.Contains(script, frag) {
			t.Errorf("runner script missing %q\n%s", frag, script)
		}
	}
}

func runRunnerScript(t *testing.T, base, pending, done, failed, fileName, cmd, args string) string {
	t.Helper()
	os.MkdirAll(pending, 0755)
	os.WriteFile(filepath.Join(pending, fileName), []byte("-- noop --"), 0644)

	r := RunnerTemplate{
		Command:      cmd,
		ArgsTemplate: args,
		Pending:      pending,
		Done:         done,
		Failed:       failed,
	}
	script, err := r.RenderRunner()
	if err != nil {
		t.Fatalf("RenderRunner: %v", err)
	}
	scriptPath := filepath.Join(base, "run.sh")
	os.WriteFile(scriptPath, []byte(script), 0755)
	out, _ := exec.Command("sh", scriptPath).CombinedOutput()
	return string(out)
}

func TestRenderRunnerMovesToDoneOnSuccess(t *testing.T) {
	base := t.TempDir()
	pending := filepath.Join(base, "pending")
	done := filepath.Join(base, "done")
	failed := filepath.Join(base, "failed")
	fileName := BatchFileName(1, time.Now(), time.Now(), "EMP", 1, 1, false)

	out := runRunnerScript(t, base, pending, done, failed, fileName, "true", "{file}")
	if _, err := os.Stat(filepath.Join(done, fileName)); err != nil {
		t.Errorf("expected batch moved to done/: %v (out=%s)", err, out)
	}
	if _, err := os.Stat(filepath.Join(pending, fileName)); !os.IsNotExist(err) {
		t.Errorf("expected batch removed from pending/")
	}
}

func TestRenderRunnerMovesToFailedOnError(t *testing.T) {
	base := t.TempDir()
	pending := filepath.Join(base, "pending")
	done := filepath.Join(base, "done")
	failed := filepath.Join(base, "failed")
	fileName := BatchFileName(1, time.Now(), time.Now(), "EMP", 1, 1, false)

	runRunnerScript(t, base, pending, done, failed, fileName, "false", "{file}")
	if _, err := os.Stat(filepath.Join(failed, fileName)); err != nil {
		t.Errorf("expected batch moved to failed/: %v", err)
	}
}
