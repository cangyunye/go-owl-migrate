package extractor

import (
	"testing"

	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

func TestCapabilities(t *testing.T) {
	tests := []struct {
		dbType string
		has    []string // 期望支持
		lacks  []string // 期望不支持
	}{
		{"oracle", []string{"tables", "sequences", "synonyms", "mviews", "packages", "package_bodies", "functions"}, nil},
		{"oceanbase-oracle", []string{"tables", "sequences", "synonyms", "mviews", "packages", "functions"}, nil},
		{"postgres", []string{"tables", "sequences", "mviews", "functions", "views"}, []string{"synonyms", "packages", "package_bodies"}},
		{"mysql", []string{"tables", "views", "functions", "triggers"}, []string{"sequences", "mviews", "synonyms", "packages", "package_bodies"}},
		{"oceanbase-mysql", []string{"tables", "functions"}, []string{"packages", "package_bodies", "synonyms", "mviews"}},
	}
	for _, tt := range tests {
		t.Run(tt.dbType, func(t *testing.T) {
			caps := Capabilities(tt.dbType)
			for _, h := range tt.has {
				if !caps.Contains(md.ObjectType(h)) {
					t.Errorf("%s should support %s", tt.dbType, h)
				}
			}
			for _, l := range tt.lacks {
				if caps.Contains(md.ObjectType(l)) {
					t.Errorf("%s should NOT support %s", tt.dbType, l)
				}
			}
		})
	}
}
