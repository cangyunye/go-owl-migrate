package cmd

import (
	"testing"

	"github.com/cangyunye/go-owl-migrate/internal/config"
)

func TestToBuildOptions(t *testing.T) {
	cfg := &config.Config{}
	cfg.DDL.TargetDialect = "postgres"
	cfg.DDL.IncludeComments = true
	cfg.DDL.IncludeIfNotExists = true
	cfg.DDL.IncludeDrop = true
	cfg.DDL.AddRowIDColumn = true
	cfg.DDL.IdentityToSerial = true
	cfg.DDL.NoQuoteIdentifiers = true
	cfg.DDL.Partition.Migrate = true
	cfg.DDL.SchemaMapping = map[string]string{"a": "b"}
	cfg.DDL.TypeOverrides = map[string]string{"X": "Y"}
	cfg.DDL.BooleanMapping = map[string]bool{"Y": true}
	cfg.DDL.EmptyStringToNull = true

	opts := toBuildOptions(cfg)
	if opts.TargetDialect != "postgres" || !opts.IncludeComments || !opts.IncludeIfNotExists ||
		!opts.IncludeDrop || !opts.AddRowIDColumn || !opts.IdentityToSerial || !opts.NoQuoteIdentifiers ||
		!opts.EmptyStringToNull {
		t.Errorf("scalar fields not mapped: %+v", opts)
	}
	if opts.SkipPartitions {
		t.Error("SkipPartitions should be false when partition.migrate=true")
	}
	if opts.SchemaMapping["a"] != "b" || opts.TypeOverrides["X"] != "Y" || !opts.BooleanMapping["Y"] {
		t.Errorf("map fields not mapped: %+v", opts)
	}
}
