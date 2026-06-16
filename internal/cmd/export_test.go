package cmd

import (
	"strings"
	"testing"
)

func TestExportCommandRegistered(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"export"})
	if err != nil {
		t.Fatalf("export command not found: %v", err)
	}
	if cmd == nil || cmd.Use != "export" {
		t.Fatalf("expected export command, got %v", cmd)
	}
}

func TestExportDDLSubcommand(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"export", "ddl"})
	if err != nil {
		t.Fatalf("export ddl subcommand not found: %v", err)
	}
	if cmd == nil || cmd.Use != "ddl" {
		t.Fatalf("expected export ddl command, got Use=%q", cmd.Use)
	}
	// Verify flags
	outputFlag := cmd.Flag("output")
	if outputFlag == nil {
		t.Fatal("export ddl should have --output flag")
	}
	noQuoteFlag := cmd.Flag("no-quote-identifiers")
	if noQuoteFlag == nil {
		t.Fatal("export ddl should have --no-quote-identifiers flag")
	}
}

func TestExportDataSubcommand(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"export", "data"})
	if err != nil {
		t.Fatalf("export data subcommand not found: %v", err)
	}
	if cmd == nil || cmd.Use != "data" {
		t.Fatalf("expected export data command, got Use=%q", cmd.Use)
	}
	// Should have --output flag
	outputFlag := cmd.Flag("output")
	if outputFlag == nil {
		t.Fatal("export data should have --output flag")
	}
	noQuoteFlag := cmd.Flag("no-quote-identifiers")
	if noQuoteFlag == nil {
		t.Fatal("export data should have --no-quote-identifiers flag")
	}
}

func TestExportDataDescription(t *testing.T) {
	cmd, _, _ := rootCmd.Find([]string{"export", "data"})
	if cmd == nil {
		t.Fatal("export data command not found")
	}
	if !strings.Contains(cmd.Short, "Export") {
		t.Errorf("export data short description should mention export, got %q", cmd.Short)
	}
}

func TestExportInsertSubcommand(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"export", "insert"})
	if err != nil {
		t.Fatalf("export insert subcommand not found: %v", err)
	}
	if cmd == nil || cmd.Use != "insert" {
		t.Fatalf("expected export insert command, got Use=%q", cmd.Use)
	}
	// Should have --data flag
	dataFlag := cmd.Flag("data")
	if dataFlag == nil {
		t.Fatal("export insert should have --data flag")
	}
	// Should have --dialect flag
	dialectFlag := cmd.Flag("dialect")
	if dialectFlag == nil {
		t.Fatal("export insert should have --dialect flag")
	}
}

func TestGenDDLIsAlias(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"gen-ddl"})
	if err != nil {
		t.Fatalf("gen-ddl command not found: %v", err)
	}
	if cmd == nil {
		t.Fatal("gen-ddl command not found")
	}
	if !cmd.Hidden {
		t.Error("gen-ddl should be hidden")
	}
}

func TestGenInsertIsAlias(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"gen-insert"})
	if err != nil {
		t.Fatalf("gen-insert command not found: %v", err)
	}
	if cmd == nil {
		t.Fatal("gen-insert command not found")
	}
	if !cmd.Hidden {
		t.Error("gen-insert should be hidden")
	}
}

func TestExportParentHasSubcommands(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"export"})
	if err != nil {
		t.Fatalf("export command not found: %v", err)
	}
	subs := cmd.Commands()
	if len(subs) == 0 {
		t.Fatal("export command should have subcommands")
	}
	found := map[string]bool{}
	for _, sub := range subs {
		found[sub.Use] = true
	}
	if !found["ddl"] {
		t.Error("export should have ddl subcommand")
	}
	if !found["data"] {
		t.Error("export should have data subcommand")
	}
	if !found["insert"] {
		t.Error("export should have insert subcommand")
	}
}
