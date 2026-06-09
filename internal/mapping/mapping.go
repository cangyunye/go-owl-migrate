package mapping

import (
	"fmt"
	"io"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// TypeMappingFile is the YAML structure for a type mapping file.
type TypeMappingFile struct {
	Version    string            `yaml:"version"`
	Name       string            `yaml:"name"`
	SourceDB   string            `yaml:"source_db"`
	TargetDB   string            `yaml:"target_db"`
	Description string           `yaml:"description"`

	ExactMappings      map[string]string      `yaml:"exact_mappings"`
	Parameterized      map[string][]ParamRule  `yaml:"parameterized"`
	SemanticOverrides  []SemanticOverride      `yaml:"semantic_overrides"`
	DefaultTransforms  map[string]string       `yaml:"default_transforms"`
}

// ParamRule is a parameterized type mapping rule.
type ParamRule struct {
	Condition ParamCondition `yaml:"condition"`
	Target    string         `yaml:"target"`
}

// ParamCondition defines when a parameterized rule matches.
// Use pointers so we can distinguish "not set" from "value 0".
type ParamCondition struct {
	Scale        *int `yaml:"scale"`
	ScaleGT      *int `yaml:"scale_gt"`
	MaxPrecision *int `yaml:"max_precision"`
	MinPrecision *int `yaml:"min_precision"`
	MaxLength    *int `yaml:"max_length"`
	MinLength    *int `yaml:"min_length"`
	Default      bool `yaml:"default"`
}

// SemanticOverride maps column name patterns to target types.
type SemanticOverride struct {
	Pattern    string         `yaml:"pattern"`
	Condition  SemanticCond   `yaml:"condition"`
	TargetType string         `yaml:"target_type"`
	Transform  string         `yaml:"transform"`

	compiled *regexp.Regexp // compiled pattern
}

// SemanticCond restricts when a semantic override applies.
type SemanticCond struct {
	Type   string   `yaml:"type"`
	Length int      `yaml:"length"`
	Values []string `yaml:"values"`
}

// ── Public API ──

// LoadTypeMapping reads a YAML type mapping from a reader.
func LoadTypeMapping(r io.Reader) (*TypeMappingFile, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}

	var tm TypeMappingFile
	if err := yaml.Unmarshal(data, &tm); err != nil {
		return nil, fmt.Errorf("parse YAML: %w", err)
	}

	// Compile regex patterns
	for i := range tm.SemanticOverrides {
		var err error
		tm.SemanticOverrides[i].compiled, err = regexp.Compile(tm.SemanticOverrides[i].Pattern)
		if err != nil {
			return nil, fmt.Errorf("compile pattern %q: %w", tm.SemanticOverrides[i].Pattern, err)
		}
	}

	if tm.ExactMappings == nil {
		tm.ExactMappings = make(map[string]string)
	}

	return &tm, nil
}

// MapType maps a source type to a target type, considering precision/scale/length.
// Priority chain: parameterized (if rules exist for this type) → exact_mappings → fallback.
// If both exact and parameterized exist for a type, parameterized takes priority
// because exact_mappings is the catch-all and parameterized provides conditional rules.
func (tm *TypeMappingFile) MapType(sourceType string, length, precision, scale int) string {
	upper := strings.ToUpper(sourceType)

	// 1. Try parameterized rules FIRST (more specific than exact)
	if rules, ok := tm.Parameterized[upper]; ok {
		for _, rule := range rules {
			if tm.matchCondition(rule.Condition, length, precision, scale) {
				return applyParams(rule.Target, length, precision, scale)
			}
		}
	}
	// Also try lower-case key for parameterized
	if rules, ok := tm.Parameterized[sourceType]; ok {
		for _, rule := range rules {
			if tm.matchCondition(rule.Condition, length, precision, scale) {
				return applyParams(rule.Target, length, precision, scale)
			}
		}
	}

	// 2. Try exact mapping (catch-all)
	if target, ok := tm.ExactMappings[upper]; ok {
		return applyParams(target, length, precision, scale)
	}

	// 3. Fallback
	return applyParams(sourceType, length, precision, scale)
}

// ApplySemanticOverride returns a target type if the column matches a semantic pattern.
// Returns empty string if no semantic override matches.
func (tm *TypeMappingFile) ApplySemanticOverride(colName, sourceType string, length, precision, scale int) string {
	for _, so := range tm.SemanticOverrides {
		if !so.compiled.MatchString(colName) {
			continue
		}
		// Check type condition
		if so.Condition.Type != "" && !strings.EqualFold(so.Condition.Type, sourceType) {
			continue
		}
		if so.Condition.Length > 0 && length != so.Condition.Length {
			continue
		}
		return so.TargetType
	}
	return ""
}

// ── Internal helpers ──

func (tm *TypeMappingFile) matchCondition(c ParamCondition, length, precision, scale int) bool {
	if c.Default {
		return true
	}
	if c.Scale != nil && scale != *c.Scale {
		return false
	}
	if c.ScaleGT != nil && scale <= *c.ScaleGT {
		return false
	}
	if c.MaxPrecision != nil && precision > *c.MaxPrecision {
		return false
	}
	if c.MinPrecision != nil && precision < *c.MinPrecision {
		return false
	}
	if c.MaxLength != nil && length > *c.MaxLength {
		return false
	}
	if c.MinLength != nil && length < *c.MinLength {
		return false
	}
	return true
}

func applyLength(targetType string, length int) string {
	if length > 0 && strings.Contains(targetType, "%l") {
		return strings.ReplaceAll(targetType, "%l", fmt.Sprintf("%d", length))
	}
	return targetType
}

func applyParams(targetType string, length, precision, scale int) string {
	s := targetType
	if strings.Contains(s, "%l") {
		if length > 0 {
			s = strings.ReplaceAll(s, "%l", fmt.Sprintf("%d", length))
		} else {
			// No length value; return the type without placeholder
			s = strings.ReplaceAll(s, "(%l)", "")
			s = strings.ReplaceAll(s, "%l", "")
		}
	}
	if strings.Contains(s, "%p") {
		s = strings.ReplaceAll(s, "%p", fmt.Sprintf("%d", precision))
	}
	if strings.Contains(s, "%s") {
		s = strings.ReplaceAll(s, "%s", fmt.Sprintf("%d", scale))
	}
	return s
}
