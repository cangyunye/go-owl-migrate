package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cangyunye/go-owl-migrate/internal/cdc"
	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/dialect"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
	"github.com/cangyunye/go-owl-migrate/internal/registry"
)

// onlineCmd is the parent for the online incremental migration commands.
func onlineCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "online",
		Short: "Online incremental migration via trigger CDC",
		Long: `Online incremental migration: install changelog tables + sync triggers
on the source, then replay captured INSERT/UPDATE/DELETE/TRUNCATE to the target.

Subcommands: init (install capture), sync (replay), status, init-runner, archive.`,
	}
	cmd.AddCommand(onlineInitCmd())
	cmd.AddCommand(onlineSyncCmd())
	cmd.AddCommand(onlineStatusCmd())
	cmd.AddCommand(onlineInitRunnerCmd())
	cmd.AddCommand(onlineArchiveCmd())
	return cmd
}

// onlineInitCmd installs changelog tables and sync triggers on the source.
func onlineInitCmd() *cobra.Command {
	var apply bool
	var tables []string
	var requireKey bool
	var outDir string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Generate/install changelog tables and sync triggers on the source",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			if cmd.Flags().Changed("apply") {
				cfg.Online.CDC.Apply = apply
			}
			if cmd.Flags().Changed("require-key") {
				cfg.Online.CDC.RequireKey = requireKey
			}
			if cmd.Flags().Changed("tables") {
				cfg.Online.CDC.Tables = tables
			}
			if outDir != "" {
				cfg.Online.CDC.ScriptDir = outDir
			}
			return runOnlineInit(cmd.Context(), cfg)
		},
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "install DDL directly on the source (default: write script files)")
	cmd.Flags().StringSliceVar(&tables, "tables", nil, "tables to process (default: all)")
	cmd.Flags().BoolVar(&requireKey, "require-key", false, "fail init if any table has no primary/unique key")
	cmd.Flags().StringVarP(&outDir, "script-dir", "o", "", "output directory for generated scripts (default: online.cdc.script_dir)")
	return cmd
}

func runOnlineInit(ctx context.Context, cfg *config.Config) error {
	// Source dialect is derived from metadata: for CSV metadata we need the
	// source dialect name; resolve from ddl.source_dialect or source.type.
	srcName := cfg.DDL.SourceDialect
	if srcName == "" {
		srcName = cfg.Source.Type
	}
	src, err := registry.Get(srcName)
	if err != nil {
		return fmt.Errorf("get source dialect %q: %w", srcName, err)
	}

	// The apply path needs a live source connection; metadata must be a
	// database source so the tables actually exist.
	if cfg.Online.CDC.Apply && cfg.Metadata.Type != "database" {
		return fmt.Errorf("online init --apply requires metadata.type=database (tables must exist on the source)")
	}

	sm, err := loadSchemaModel(cfg)
	if err != nil {
		return err
	}

	tables := sm.GetTables()
	if len(tables) == 0 {
		return fmt.Errorf("no tables found in metadata")
	}

	// Filter by configured table list (case-insensitive).
	filter := cfg.Online.CDC.Tables
	if len(filter) > 0 {
		tables = filterOnlineTables(tables, filter)
		if len(tables) == 0 {
			return fmt.Errorf("no tables matched the --tables filter %v", filter)
		}
	}

	opts := dialect.CDCOptions{
		SchemaMapping:   cfg.DDL.SchemaMapping,
		ChangelogPrefix: cfg.Online.CDC.ChangelogPrefix,
	}

	var warnings []string
	var blocks []string
	for _, tbl := range tables {
		if cfg.Online.CDC.RequireKey && len(tbl.GetPrimaryKeys()) == 0 {
			return fmt.Errorf("--require-key: table %s.%s has no primary key", tbl.TableSchema, tbl.TableName)
		}
		if len(tbl.GetPrimaryKeys()) == 0 {
			warnings = append(warnings, fmt.Sprintf("table %s.%s has no primary key; UPDATE/DELETE will match on all columns", tbl.TableSchema, tbl.TableName))
		}

		b, ok := src.CDCBuilder.(interface {
			BuildChangelogTable(*md.TableDef, dialect.CDCOptions) (string, error)
			BuildSyncTrigger(*md.TableDef, dialect.CDCOptions) (string, error)
		})
		if !ok || src.CDCBuilder == nil {
			return fmt.Errorf("source dialect %q does not support trigger CDC capture", srcName)
		}
		chg, err := b.BuildChangelogTable(tbl, opts)
		if err != nil {
			return fmt.Errorf("build changelog for %s.%s: %w", tbl.TableSchema, tbl.TableName, err)
		}
		trg, err := b.BuildSyncTrigger(tbl, opts)
		if err != nil {
			return fmt.Errorf("build trigger for %s.%s: %w", tbl.TableSchema, tbl.TableName, err)
		}
		blocks = append(blocks, "-- ===== "+tbl.TableSchema+"."+tbl.TableName+" =====\n"+chg+"\n\n"+trg)
	}

	for _, w := range warnings {
		fmt.Println("WARN:", w)
	}

	if cfg.Online.CDC.Apply {
		return applyOnlineDDL(ctx, cfg, strings.Join(blocks, "\n\n\n"))
	}
	return writeOnlineScripts(cfg.Online.CDC.ScriptDir, blocks)
}

func applyOnlineDDL(ctx context.Context, cfg *config.Config, block string) error {
	db, err := openDB(cfg.Source)
	if err != nil {
		return fmt.Errorf("connect source: %w", err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping source: %w", err)
	}
	for _, stmt := range cdc.SplitSQLStatements(block) {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("apply DDL: %w\nstmt: %s", err, stmt)
		}
	}
	fmt.Printf("Applied changelog + triggers for %d table(s) on source\n", strings.Count(block, "====="))
	return nil
}

func writeOnlineScripts(dir string, blocks []string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create script dir %s: %w", dir, err)
	}
	for i, b := range blocks {
		fname := filepath.Join(dir, fmt.Sprintf("online_cdc_%03d.sql", i+1))
		if err := os.WriteFile(fname, []byte(b), 0644); err != nil {
			return fmt.Errorf("write %s: %w", fname, err)
		}
		fmt.Printf("  %s\n", fname)
	}
	fmt.Printf("Generated %d script file(s) to %s — review and execute manually, or rerun with --apply\n", len(blocks), dir)
	return nil
}

func filterOnlineTables(tables []*md.TableDef, names []string) []*md.TableDef {
	set := make(map[string]bool)
	for _, n := range names {
		set[strings.ToLower(n)] = true
	}
	var out []*md.TableDef
	for _, t := range tables {
		if set[strings.ToLower(t.TableName)] {
			out = append(out, t)
		} else if set[strings.ToLower(t.TableSchema+"."+t.TableName)] {
			out = append(out, t)
		}
	}
	return out
}
