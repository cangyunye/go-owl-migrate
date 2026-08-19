package config

import (
	"testing"
)

func TestOnlineConfig_Defaults(t *testing.T) {
	var cfg Config
	cfg.applyDefaults()
	o := cfg.Online
	if o.CDC.ChangelogPrefix != "owl_chg_" {
		t.Errorf("ChangelogPrefix = %q, want owl_chg_", o.CDC.ChangelogPrefix)
	}
	if o.CDC.ScriptDir != "./output/online/" {
		t.Errorf("ScriptDir = %q, want ./output/online/", o.CDC.ScriptDir)
	}
	if o.Sync.PollInterval != "1s" {
		t.Errorf("PollInterval = %q, want 1s", o.Sync.PollInterval)
	}
	if o.Sync.BatchSize != 500 {
		t.Errorf("BatchSize = %d, want 500", o.Sync.BatchSize)
	}
	if o.Sync.OnError != "skip" {
		t.Errorf("OnError = %q, want skip", o.Sync.OnError)
	}
	if o.Sync.ErrorTable != "owl_sync_error" {
		t.Errorf("ErrorTable = %q, want owl_sync_error", o.Sync.ErrorTable)
	}
	if o.Archive.Format != "tar.gz" {
		t.Errorf("Archive.Format = %q, want tar.gz", o.Archive.Format)
	}
	if o.Archive.Dir != "./online/archive/" {
		t.Errorf("Archive.Dir = %q, want ./online/archive/", o.Archive.Dir)
	}
	if o.State.DB != "./output/online/online.db" {
		t.Errorf("State.DB = %q, want ./output/online/online.db", o.State.DB)
	}
}

func TestOnlineConfig_Validate(t *testing.T) {
	base := func(o OnlineConfig) Config {
		c := Config{
			Metadata: MetadataConfig{Type: "csv"},
			DDL:      DDLConfig{TargetDialect: "postgres"},
			Online:   o,
		}
		c.applyDefaults()
		return c
	}
	tests := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{"valid on_error skip", base(OnlineConfig{Sync: OnlineSyncConfig{OnError: "skip"}}), ""},
		{"valid on_error stop", base(OnlineConfig{Sync: OnlineSyncConfig{OnError: "stop"}}), ""},
		{"valid on_error retry", base(OnlineConfig{Sync: OnlineSyncConfig{OnError: "retry"}}), ""},
		{"bad on_error", base(OnlineConfig{Sync: OnlineSyncConfig{OnError: "bogus"}}), "online.sync.on_error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.cfg.applyDefaults()
			err := tt.cfg.validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validate() unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validate() = nil, want error containing %q", tt.wantErr)
			}
			if !contains(err.Error(), tt.wantErr) {
				t.Errorf("validate() error %q, want containing %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestOnlineConfig_ArchiveIsEnabledByDefault(t *testing.T) {
	var cfg Config
	cfg.applyDefaults()
	if !cfg.Online.Archive.Enabled {
		t.Error("online.archive.enabled should default to true")
	}
}

func TestDBConfig_AdapterIsZero(t *testing.T) {
	tests := []struct {
		name string
		cfg  DBConfig
		want bool
	}{
		{"no adapter", DBConfig{Type: "postgres", DSN: "x"}, true},
		{"adapter set", DBConfig{Type: "somepg", Adapter: "./adapters/somepg.yaml"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.AdapterIsZero(); got != tt.want {
				t.Errorf("AdapterIsZero() = %v, want %v", got, tt.want)
			}
		})
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
