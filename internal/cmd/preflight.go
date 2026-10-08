package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/plancheck"
	"github.com/cangyunye/go-owl-migrate/internal/service"
)

// preflightCmd validates a migration plan against the live databases without
// executing anything: metadata loadable, table selection matches, offline
// metadata tables readable through the source account, schema_mapping
// coverage, target DDL permission. The same checks run automatically as
// migrate's Step 2 and behind the web UI's preflight.
func preflightCmd() *cobra.Command {
	var skipTarget bool

	cmd := &cobra.Command{
		Use:   "preflight",
		Short: "Validate the migration plan against live databases (read-only)",
		Long: `Runs every plan-level check without executing anything:

  1. Metadata loadable (csv / xlsx / database) and the table selection matches
  2. Offline metadata (csv/xlsx) tables are readable through the source account
     — catches "connection OK but SCOTT.EMP nowhere to be found" mismatches
  3. ddl.schema_mapping covers every metadata schema (warns on silent fallback)
  4. Target account can CREATE/DROP a probe table (warns on missing grants)

Exit code 1 when a blocking check fails. Warnings do not block.`,
		Example: `  owl-migrate preflight -c migrate.yaml
  owl-migrate preflight -c migrate.yaml --skip-target   # export / sql-out plans`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfigFile(false)
			if err != nil {
				return err
			}
			applyChannelOverrides(cfg)

			fmt.Println("=== Plan preflight ===")
			sm, err := loadSchemaModel(cfg)
			if err != nil {
				return fmt.Errorf("metadata: %w", err)
			}
			matched := filterTables(sm.GetTables(), cfg.Export.Tables.Include)
			fmt.Printf("Metadata: %d tables loaded, %d matched by include filter\n",
				len(sm.GetTables()), len(matched))

			skip := skipTarget || service.DetectScenario(cfg) == "export"
			report := plancheck.Run(cmd.Context(), cfg, matched, plancheck.Options{
				SkipTarget:   skip,
				TruncateWarn: truncateWarnText(cfg),
			})
			printPlanChecks(report)
			if !report.OK() {
				return fmt.Errorf("plan preflight failed:\n%s", report.Failures())
			}
			fmt.Println("Preflight passed — the plan is consistent with the live databases.")
			return nil
		},
	}

	cmd.Flags().BoolVar(&skipTarget, "skip-target", false, "skip target-side checks (export / SQL-output plans)")
	return cmd
}

// printPlanChecks renders a report in the shared step-output style.
func printPlanChecks(report *plancheck.Report) {
	for _, c := range report.Checks {
		switch c.Status {
		case plancheck.StatusFail:
			fmt.Printf("  ✗ %s: %s\n", c.Name, c.Detail)
		case plancheck.StatusWarn:
			fmt.Printf("  ⚠ %s: %s\n", c.Name, c.Detail)
		default:
			fmt.Printf("  ✓ %s: %s\n", c.Name, c.Detail)
		}
	}
	for _, w := range report.Warns {
		fmt.Printf("  ⚠ %s\n", w)
	}
}

// applyChannelOverrides mirrors openDB's flag-over-config behavior for the
// whole plan: CLI --channel/--jars-dir win over the yaml on both endpoints.
func applyChannelOverrides(cfg *config.Config) {
	if channelFlag != "" {
		cfg.Source.Channel = channelFlag
		cfg.Target.Channel = channelFlag
	}
	if jarsDirFlag != "" {
		cfg.Source.Agent.JarsDir = jarsDirFlag
		cfg.Target.Agent.JarsDir = jarsDirFlag
	}
}

// truncateWarnText is the destructive-operation warning shared by the CLI
// preflight, migrate Step 2, and the web preflight.
func truncateWarnText(cfg *config.Config) string {
	if cfg.Import.Target.TruncateBefore {
		return "目标表将在导入前执行 TRUNCATE（import.target.truncate_before）"
	}
	return ""
}
