package generator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cangyunye/go-owl-migrate/internal/dialect"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

// DDLGenerator orchestrates DDL generation from a SchemaModel using a Dialect.
type DDLGenerator struct {
	dialect  dialect.Dialect
	opts     dialect.BuildOptions
	outputDir string
}

// NewDDLGenerator creates a DDL generator.
func NewDDLGenerator(d dialect.Dialect, opts dialect.BuildOptions, outputDir string) *DDLGenerator {
	return &DDLGenerator{
		dialect:   d,
		opts:      opts,
		outputDir: outputDir,
	}
}

// GenerateTables generates CREATE TABLE DDL for all tables in the model.
func (g *DDLGenerator) GenerateTables(sm *md.SchemaModel) ([]string, error) {
	var files []string
	for _, tbl := range sm.GetTables() {
		sql, err := g.dialect.BuildCreateTable(tbl, g.opts)
		if err != nil {
			return files, fmt.Errorf("table %s.%s: %w", tbl.TableSchema, tbl.TableName, err)
		}
		if sql == "" {
			continue
		}
		path, err := g.writeFile(tbl.TableSchema, tbl.TableName, "table", sql)
		if err != nil {
			return files, err
		}
		files = append(files, path)
	}
	return files, nil
}

// GenerateIndexes generates CREATE INDEX DDL for all indexes.
func (g *DDLGenerator) GenerateIndexes(sm *md.SchemaModel) ([]string, error) {
	var files []string
	for _, tbl := range sm.GetTables() {
		for _, idx := range tbl.GetIndexes() {
			sql, err := g.dialect.BuildCreateIndex(idx)
			if err != nil {
				return files, err
			}
			if sql == "" {
				continue
			}
			path, err := g.writeFile(tbl.TableSchema, idx.IndexName, "index", sql)
			if err != nil {
				return files, err
			}
			files = append(files, path)
		}
	}
	return files, nil
}

// GenerateViews generates CREATE VIEW DDL for all views.
func (g *DDLGenerator) GenerateViews(sm *md.SchemaModel) ([]string, error) {
	var files []string
	for _, v := range sm.GetViews() {
		sql, err := g.dialect.BuildCreateView(v)
		if err != nil {
			return files, err
		}
		if sql == "" {
			continue
		}
		path, err := g.writeFile(v.ViewSchema, v.ViewName, "view", sql)
		if err != nil {
			return files, err
		}
		files = append(files, path)
	}
	return files, nil
}

// GenerateSequences generates CREATE SEQUENCE DDL.
func (g *DDLGenerator) GenerateSequences(sm *md.SchemaModel, schema string) ([]string, error) {
	var files []string
	for _, seq := range sm.GetSequences(schema) {
		sql, err := g.dialect.BuildCreateSequence(seq)
		if err != nil {
			return files, err
		}
		if sql == "" {
			continue
		}
		path, err := g.writeFile(seq.SequenceSchema, seq.SequenceName, "sequence", sql)
		if err != nil {
			return files, err
		}
		files = append(files, path)
	}
	return files, nil
}

func (g *DDLGenerator) writeFile(schema, name, objType, content string) (string, error) {
	if err := os.MkdirAll(g.outputDir, 0755); err != nil {
		return "", err
	}
	filename := fmt.Sprintf("%s.%s.%s.sql", strings.ToLower(schema), strings.ToLower(name), objType)
	path := filepath.Join(g.outputDir, filename)
	if err := os.WriteFile(path, []byte(content+"\n"), 0644); err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}
	return path, nil
}
