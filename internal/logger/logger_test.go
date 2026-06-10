package logger

import (
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func TestNew_TextFormat(t *testing.T) {
	cfg := Config{
		Level:  "info",
		Format: "text",
		File:   "",
	}
	log, err := New(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if log == nil {
		t.Fatal("expected non-nil logger")
	}
}

func TestNew_JSONFormat(t *testing.T) {
	cfg := Config{
		Level:  "debug",
		Format: "json",
		File:   "",
	}
	log, err := New(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if log == nil {
		t.Fatal("expected non-nil logger")
	}
}

func TestNew_InvalidLevel(t *testing.T) {
	cfg := Config{
		Level:  "invalid",
		Format: "text",
	}
	_, err := New(cfg)
	if err == nil {
		t.Error("expected error for invalid level")
	}
}

func TestNew_InvalidFormat(t *testing.T) {
	cfg := Config{
		Level:  "info",
		Format: "yaml",
	}
	_, err := New(cfg)
	if err == nil {
		t.Error("expected error for invalid format")
	}
}

func TestLevelFiltering(t *testing.T) {
	var buf strings.Builder
	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()),
		zapcore.AddSync(&nopWriteSyncer{&buf}),
		zapcore.InfoLevel,
	)
	log := zap.New(core)

	log.Debug("should not appear")
	log.Info("should appear")

	output := buf.String()
	if strings.Contains(output, "should not appear") {
		t.Error("DEBUG message should be filtered at INFO level")
	}
	if !strings.Contains(output, "should appear") {
		t.Error("INFO message should appear")
	}
}

func TestWithField(t *testing.T) {
	cfg := Config{Level: "debug", Format: "text"}
	log, err := New(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	child := log.With(zap.String("key", "value"))
	if child == nil {
		t.Error("expected non-nil child logger")
	}
}

func TestWithFields(t *testing.T) {
	cfg := Config{Level: "debug", Format: "text"}
	log, err := New(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	child := log.With(zap.String("a", "1"), zap.String("b", "2"))
	if child == nil {
		t.Error("expected non-nil child logger")
	}
}

func TestConfig_Defaults(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Level != "info" {
		t.Errorf("Level = %q, want info", cfg.Level)
	}
	if cfg.Format != "text" {
		t.Errorf("Format = %q, want text", cfg.Format)
	}
	if cfg.File != "" {
		t.Errorf("File = %q, want empty", cfg.File)
	}
}

type nopWriteSyncer struct{ buf *strings.Builder }

func (n *nopWriteSyncer) Write(p []byte) (int, error) { return n.buf.Write(p) }
func (n *nopWriteSyncer) Sync() error                 { return nil }
