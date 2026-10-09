package config

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestAIApplyDefaults(t *testing.T) {
	var c Config
	c.AI = AIConfig{MaxTokens: 4096}
	c.AI.ApplyDefaults()
	if c.AI.Provider != "deepseek" || c.AI.Model != "deepseek-flash" {
		t.Errorf("defaults = %+v", c.AI)
	}
	if c.AI.BaseURL != "https://api.deepseek.com" || c.AI.APIKeyEnv != "OWL_AI_API_KEY" {
		t.Errorf("defaults = %+v", c.AI)
	}
	if c.AI.Effort != "low" || c.AI.MaxTokens != 4096 || c.AI.MaxRepairRounds != 3 {
		t.Errorf("defaults = %+v", c.AI)
	}
}

func TestAIAPIKeyEnv(t *testing.T) {
	t.Setenv("OWL_AI_API_KEY", "primary")
	t.Setenv("DEEPSEEK_API_KEY", "fallback")
	a := AIConfig{APIKeyEnv: "OWL_AI_API_KEY"}
	if got := a.APIKey(); got != "primary" {
		t.Errorf("APIKey = %q", got)
	}
	a.APIKeyEnv = "OTHER_VAR"
	os.Unsetenv("OTHER_VAR")
	if got := a.APIKey(); got != "fallback" {
		t.Errorf("APIKey fallback = %q", got)
	}
	a.APIKeyEnv = "DEEPSEEK_API_KEY"
	if got := a.APIKey(); got != "fallback" {
		t.Errorf("APIKey explicit deepseek = %q", got)
	}
}

func TestAIValidate(t *testing.T) {
	base := func(c Config) Config {
		c.Metadata.Type = "csv"
		c.Metadata.CSV.Path = "/tmp/meta.csv"
		c.DDL.TargetDialect = "postgres"
		return c
	}
	c := base(Config{AI: AIConfig{Effort: "extreme"}})
	if err := c.validate(); err == nil || !strings.Contains(err.Error(), "ai.effort") {
		t.Errorf("validate = %v", err)
	}
	c = base(Config{AI: AIConfig{Provider: "acme"}})
	if err := c.validate(); err == nil || !strings.Contains(err.Error(), "ai.base_url") {
		t.Errorf("validate = %v", err)
	}
	c = base(Config{AI: AIConfig{}})
	if err := c.validate(); err != nil {
		t.Errorf("empty AI must not fail validate: %v", err)
	}
}

func TestAIMarshalRoundTrip(t *testing.T) {
	src := Config{AI: AIConfig{Model: "deepseek-v4-pro", Effort: "high"}}
	data, err := src.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML: %v", err)
	}
	out, err := yaml.Marshal(data)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	yamlText := string(out)
	if !strings.Contains(yamlText, "deepseek-v4-pro") || !strings.Contains(yamlText, "effort: high") {
		t.Errorf("yaml = %s", yamlText)
	}
	// 密钥字段永远不存在于序列化输出
	if strings.Contains(yamlText, "key") {
		t.Errorf("yaml must not contain a key field: %s", yamlText)
	}
}

func TestAIProviderPresets(t *testing.T) {
	for _, p := range []string{"deepseek", "openai", "moonshot", "qwen", "glm", "ollama", "custom"} {
		if !ValidAIProvider(p) {
			t.Errorf("preset %q not valid", p)
		}
	}
	if ValidAIProvider("anthropic") {
		t.Error("unknown provider accepted")
	}
	for name, want := range map[string]string{
		"deepseek": "https://api.deepseek.com",
		"openai":   "https://api.openai.com/v1",
		"ollama":   "http://127.0.0.1:11434/v1",
	} {
		if got := DefaultAIBaseURL[name]; got != want {
			t.Errorf("preset %s base_url = %q, want %q", name, got, want)
		}
	}
}

func TestAIApplyDefaultsModelScoping(t *testing.T) {
	var a AIConfig
	a.ApplyDefaults()
	if a.Model != "deepseek-flash" {
		t.Errorf("deepseek default model = %q", a.Model)
	}
	custom := AIConfig{Provider: "custom", BaseURL: "http://127.0.0.1:9/v1"}
	custom.ApplyDefaults()
	if custom.Model != "" {
		t.Errorf("custom provider must not inherit deepseek model, got %q", custom.Model)
	}
}
