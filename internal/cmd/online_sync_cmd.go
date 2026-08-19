package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/cangyunye/go-owl-migrate/internal/adapter"
	"github.com/cangyunye/go-owl-migrate/internal/cdc"
	"github.com/cangyunye/go-owl-migrate/internal/config"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

// changelogName mirrors the CDC builder's default changelog naming so the sync
// can locate the capture table without re-deriving from the builder.
func changelogName(cfg *config.Config, table string) string {
	prefix := cfg.Online.CDC.ChangelogPrefix
	if prefix == "" {
		prefix = "owl_chg_"
	}
	return prefix + table
}

// syncEngine holds the shared pieces of the sync loop.
type syncEngine struct {
	cfg     *config.Config
	srcName string
	adapter *adapter.Adapter
	store   cdc.StateStore
	pending string
	done    string
	failed  string
	chgQ    string // source-quoted changelog qualifier template
}

func loadSyncEngine(cfg *config.Config) (*syncEngine, error) {
	e := &syncEngine{cfg: cfg}
	e.pending = cfg.Online.Files.Pending
	e.done = cfg.Online.Files.Done
	e.failed = cfg.Online.Files.Failed
	if cfg.Online.CDC.Apply {
		e.srcName = cfg.DDL.SourceDialect
		if e.srcName == "" {
			e.srcName = cfg.Source.Type
		}
	}
	if cfg.Target.Adapter != "" {
		a, err := adapter.Load(cfg.Target.Adapter)
		if err != nil {
			return nil, err
		}
		e.adapter = a
	}
	store, err := cdc.NewJSONStateStore(statePath(cfg))
	if err != nil {
		return nil, err
	}
	e.store = store
	return e, nil
}

func statePath(cfg *config.Config) string {
	if cfg.Online.State.DB != "" {
		return cfg.Online.State.DB
	}
	return "./output/online/state.json"
}

// onlineSyncCmd runs the continuous poll→replay loop.
func onlineSyncCmd() *cobra.Command {
	var once bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Continuously poll source changelogs and replay to target",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			e, err := loadSyncEngine(cfg)
			if err != nil {
				return err
			}
			defer e.store.Close()
			for {
				if err := e.syncOnce(cmd.Context()); err != nil {
					return err
				}
				if once {
					return nil
				}
				time.Sleep(pollInterval(cfg))
			}
		},
	}
	cmd.Flags().BoolVar(&once, "once", false, "run a single poll-replay cycle then exit")
	return cmd
}

func pollInterval(cfg *config.Config) time.Duration {
	d, err := time.ParseDuration(cfg.Online.Sync.PollInterval)
	if err != nil || d <= 0 {
		return time.Second
	}
	return d
}

// syncOnce performs one pass over the tracked tables.
func (e *syncEngine) syncOnce(ctx context.Context) error {
	sm, err := loadSchemaModel(e.cfg)
	if err != nil {
		return err
	}
	tables := sm.GetTables()
	if len(tables) == 0 {
		return fmt.Errorf("no tables in metadata")
	}

	if e.adapter != nil && e.adapter.IsFileBatch() {
		return e.syncFileBatch(ctx, tables)
	}
	return e.syncNative(ctx, tables)
}

// syncFileBatch polls each changelog, spills batch files, advances filed
// checkpoints; acked comes from the done/ directory scan.
func (e *syncEngine) syncFileBatch(ctx context.Context, tables []*md.TableDef) error {
	srcDB, err := openDB(e.cfg.Source)
	if err != nil {
		return fmt.Errorf("connect source: %w", err)
	}
	defer srcDB.Close()
	if err := srcDB.PingContext(ctx); err != nil {
		return fmt.Errorf("ping source: %w", err)
	}

	quoter := e.adapter.Quoter()

	cps, _ := e.store.LoadCheckpoints()
	for _, tbl := range tables {
		if !e.shouldTrack(tbl) {
			continue
		}
		chg := changelogName(e.cfg, tbl.TableName)
		last := cps[tbl.TableName].FiledChgID
		poller := &cdc.Poller{
			DB:          srcDB,
			Changelog:   quoter(tbl.TableSchema) + "." + quoter(chg),
			BatchSize:   e.cfg.Online.Sync.BatchSize,
			Placeholder: e.adapter.PlaceholderFn(),
		}
		changes, maxID, err := poller.PollAfter(ctx, last)
		if err != nil {
			// Changelog may not exist yet; skip with warning rather than abort.
			return fmt.Errorf("poll %s.%s: %w", tbl.TableSchema, tbl.TableName, err)
		}
		if len(changes) == 0 || maxID <= last {
			continue
		}
		// Wait: Poller queries by chg_id for all modes; for file-batch the
		// placeholder may be $N (PG-style ORDER BY ... LIMIT). This requires a
		// placeholder-aware query, which the adapter provides.
		tt := e.adapter.ToTargetTable(
			tbl.TableName,
			e.targetQualified(tbl),
			columnNames(tbl),
			keyColumnNames(tbl),
		)
		bw := e.adapter.BatchWriterFor(e.pending, nil)
		if _, err := bw.Write(tbl.TableName, changes, tt, time.Now()); err != nil {
			return fmt.Errorf("spill %s: %w", tbl.TableName, err)
		}
		e.store.SaveCheckpoint(cdc.Checkpoint{TableName: tbl.TableName, FiledChgID: maxID})
	}

	// acked: derived from done/ by scanning for the highest contiguous seq per table.
	acked, err := cdc.AckedFromDone(e.done)
	if err == nil {
		for t, id := range acked {
			cp := cps[t]
			cp.AckedChgID = id
			e.store.SaveCheckpoint(cp)
		}
	}
	return nil
}

func (e *syncEngine) targetQualified(tbl *md.TableDef) string {
	schema := tbl.TableSchema
	if m, ok := e.cfg.DDL.SchemaMapping[schema]; ok {
		schema = m
	}
	q := e.adapter.Quoter()
	return q(schema) + "." + q(tbl.TableName)
}

func (e *syncEngine) shouldTrack(tbl *md.TableDef) bool {
	// Never treat changelog tables as migration sources.
	prefix := e.cfg.Online.CDC.ChangelogPrefix
	if prefix == "" {
		prefix = "owl_chg_"
	}
	if strings.HasPrefix(strings.ToUpper(tbl.TableName), strings.ToUpper(prefix)) {
		return false
	}
	if len(e.cfg.Online.CDC.Tables) == 0 {
		return true
	}
	for _, n := range e.cfg.Online.CDC.Tables {
		if strings.EqualFold(n, tbl.TableName) || strings.EqualFold(n, tbl.TableSchema+"."+tbl.TableName) {
			return true
		}
	}
	return false
}

func (e *syncEngine) syncNative(ctx context.Context, tables []*md.TableDef) error {
	// Native replay requires a target connection and is not yet wired for the
	// general adapter; report clearly.
	return fmt.Errorf("online sync native mode requires wiring (use file-batch target adapter or implement native driver)")
}

// onlineInitRunnerCmd writes the file-batch runner script.
func onlineInitRunnerCmd() *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "init-runner",
		Short: "Generate the batch runner shell script for file-batch targets",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			if cfg.Target.Adapter == "" {
				return fmt.Errorf("target.adapter is required for init-runner")
			}
			if out == "" {
				out = cfg.Online.Files.Pending + "../run_incremental.sh"
			}
			a, err := adapter.Load(cfg.Target.Adapter)
			if err != nil {
				return err
			}
			rt, err := a.RunnerTemplateFor(cfg.Online.Files.Pending, cfg.Online.Files.Done, cfg.Online.Files.Failed)
			if err != nil {
				return err
			}
			script, err := rt.RenderRunner()
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(out), 0755); err != nil {
				return err
			}
			if err := os.WriteFile(out, []byte(script), 0755); err != nil {
				return err
			}
			fmt.Printf("Wrote runner script to %s\n", out)
			return nil
		},
	}
	cmd.Flags().StringVarP(&out, "output", "o", "", "output script path (default: <pending>/../run_incremental.sh)")
	return cmd
}

// onlineArchiveCmd compresses done/ batches into tar.gz archives.
func onlineArchiveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "archive",
		Short: "Compress executed (done/) batches into tar.gz archives",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			n, err := cdc.ArchiveDone(cfg.Online.Files.Done, cfg.Online.Archive.Dir)
			if err != nil {
				return err
			}
			fmt.Printf("Archived %d done batch file(s) to %s\n", n, cfg.Online.Archive.Dir)
			return nil
		},
	}
}

// onlineStatusCmd prints a checkpoint/coverage summary.
func onlineStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show given/tracked table sync checkpoints and directory counts",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			e, err := loadSyncEngine(cfg)
			if err != nil {
				return err
			}
			defer e.store.Close()
			cps, _ := e.store.LoadCheckpoints()
			fmt.Println("=== Online sync status ===")
			if len(cps) == 0 {
				fmt.Println("  no checkpoints yet")
			}
			for t, cp := range cps {
				fmt.Printf("  %-20s filed=%-12d acked=%d\n", t, cp.FiledChgID, cp.AckedChgID)
			}
			fmt.Printf("  pending: %d  done: %d  failed: %d\n",
				fileCount(e.pending), fileCount(e.done), fileCount(e.failed))
			return nil
		},
	}
}

func fileCount(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	return len(entries)
}

func columnNames(tbl *md.TableDef) []string {
	var names []string
	for _, c := range tbl.GetColumns() {
		names = append(names, c.ColumnName)
	}
	return names
}

func keyColumnNames(tbl *md.TableDef) []string {
	var names []string
	for _, k := range tbl.GetPrimaryKeys() {
		names = append(names, k.ColumnName)
	}
	return names
}
