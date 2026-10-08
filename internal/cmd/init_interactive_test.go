package cmd

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cangyunye/go-owl-migrate/internal/config"
)

// withStdin swaps os.Stdin for a pipe carrying input ("" → closed immediately,
// i.e. EOF) and restores it on cleanup. Interactive helpers must never touch
// the real terminal under `go test`.
func withStdin(t *testing.T, input string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	if _, err := w.WriteString(input); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	w.Close() // close after writing so readers see EOF at end of input
	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = old
		r.Close()
	})
}

// TestAskChoiceEOFDoesNotLoop asserts the historical hang is gone: EOF must
// return errEOFInteractive — with or without a default (uniform "stdin ended
// = abort" semantics, never a silent default-laden config).
func TestAskChoiceEOFDoesNotLoop(t *testing.T) {
	r := bufio.NewReader(strings.NewReader(""))
	if _, err := askChoice(r, "pick", []string{"a", "b"}, ""); err == nil {
		t.Fatal("askChoice with EOF and no default must error, got nil")
	}
	r2 := bufio.NewReader(strings.NewReader(""))
	if _, err := askChoice(r2, "pick", []string{"a", "b"}, "b"); err == nil {
		t.Fatal("askChoice with EOF and a default must also error (no silent defaults)")
	}
	// A blank line (stream still open) falls back to the default.
	r3 := bufio.NewReader(strings.NewReader("\n"))
	got, err := askChoice(r3, "pick", []string{"a", "b"}, "b")
	if err != nil || got != "b" {
		t.Fatalf("askChoice blank line with default = %q, %v; want b, nil", got, err)
	}
}

// TestAskHelpersEOF asserts ask/askYesNo/askTables propagate EOF instead of
// silently producing default-laden configs in non-interactive runs.
func TestAskHelpersEOF(t *testing.T) {
	empty := func() *bufio.Reader { return bufio.NewReader(strings.NewReader("")) }
	if _, err := ask(empty(), "q", "d"); err == nil {
		t.Error("ask must error on EOF")
	}
	if _, err := askYesNo(empty(), "q?", true); err == nil {
		t.Error("askYesNo must error on EOF")
	}
	if _, err := askTables(empty(), "tables"); err == nil {
		t.Error("askTables must error on EOF")
	}
	if _, err := askDialect(empty(), "dialect", ""); err == nil {
		t.Error("askDialect must error on EOF")
	}
}

// TestReadLineLastLineWithoutNewline: the final line of a stream often has no
// trailing \n; it must still be processed.
func TestReadLineLastLineWithoutNewline(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("migrate"))
	got, err := readLine(r)
	if err != nil || got != "migrate" {
		t.Fatalf("readLine = %q, %v; want migrate, nil", got, err)
	}
	// Next read is EOF.
	if _, err := readLine(r); err == nil {
		t.Fatal("second read must be EOF")
	}
}

// TestRunInteractiveEOFExitsCleanly drives the full wizard with a closed stdin:
// it must return the friendly non-interactive error, not hang.
func TestRunInteractiveEOFExitsCleanly(t *testing.T) {
	withStdin(t, "")
	err := runInteractive(filepath.Join(t.TempDir(), "migrate.yaml"))
	if err == nil {
		t.Fatal("runInteractive with closed stdin must error")
	}
	if !strings.Contains(err.Error(), "stdin closed") ||
		!strings.Contains(err.Error(), "--source-dsn") {
		t.Fatalf("error should point at non-interactive mode, got: %v", err)
	}
}

// TestInteractiveExportMetadataFormatChoice asserts the wizard's output-format
// answer reaches the file header's next-command hint instead of being dropped
// (the historical bug), and that the generated config still loads.
func TestInteractiveExportMetadataFormatChoice(t *testing.T) {
	withStdin(t, "mysql\nuser:pass@tcp(127.0.0.1:3306)/shop\nshop\nxlsx\n")
	out := filepath.Join(t.TempDir(), "migrate.yaml")
	if err := interactiveExportMetadata(bufio.NewReader(os.Stdin), out); err != nil {
		t.Fatalf("interactiveExportMetadata: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "--format xlsx") {
		t.Errorf("header hint must carry the user's format choice:\n%s", text)
	}
	if _, err := config.Load(out); err != nil {
		t.Fatalf("generated config must load: %v", err)
	}
}

// TestInteractiveExportMetadataInvalidFormatReprompts: an unknown format loops
// back for input; EOF during the loop aborts with the standard error.
func TestInteractiveExportMetadataInvalidFormatReprompts(t *testing.T) {
	withStdin(t, "mysql\nuser:pass@tcp(127.0.0.1:3306)/shop\nshop\nnope\nxlsx\n")
	out := filepath.Join(t.TempDir(), "migrate.yaml")
	if err := interactiveExportMetadata(bufio.NewReader(os.Stdin), out); err != nil {
		t.Fatalf("invalid format then valid must succeed: %v", err)
	}
	data, _ := os.ReadFile(out)
	if !strings.Contains(string(data), "--format xlsx") {
		t.Error("format choice after re-prompt lost")
	}
}

// TestWriteConfigOverwriteConfirmation: an existing target asks first; EOF
// (non-interactive) refuses and points at --force; --force overwrites.
func TestWriteConfigOverwriteConfirmation(t *testing.T) {
	cfg := &config.Config{General: config.GeneralConfig{LogLevel: "info"}}
	path := filepath.Join(t.TempDir(), "migrate.yaml")
	if err := os.WriteFile(path, []byte("# original\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Non-interactive (EOF on confirm): refuse, mention --force.
	withStdin(t, "")
	oldForce := initForce
	initForce = false
	defer func() { initForce = oldForce }()
	if err := writeConfig(cfg, path); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("overwrite without --force under EOF must fail with --force hint, got: %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "# original\n" {
		t.Errorf("original file must be untouched, got %q", data)
	}

	// Interactive "n": keep the file.
	withStdin(t, "n\n")
	if err := writeConfig(cfg, path); err == nil || !strings.Contains(err.Error(), "kept the existing file") {
		t.Fatalf("answering n must keep the file, got: %v", err)
	}

	// --force overwrites unconditionally, no stdin read.
	withStdin(t, "")
	initForce = true
	if err := writeConfig(cfg, path); err != nil {
		t.Fatalf("writeConfig with --force: %v", err)
	}
	if data, _ := os.ReadFile(path); strings.Contains(string(data), "# original") {
		t.Error("--force must overwrite the file")
	}
}
