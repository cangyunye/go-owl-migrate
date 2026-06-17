package generator

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cangyunye/go-owl-migrate/internal/dialect"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

// DDLGenerator orchestrates DDL generation from a SchemaModel using a Dialect.
type DDLGenerator struct {
	dialect   dialect.Dialect
	opts      dialect.BuildOptions
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
// IndexDefs are grouped by IndexName to support composite (multi-column) indexes.
func (g *DDLGenerator) GenerateIndexes(sm *md.SchemaModel) ([]string, error) {
	var files []string
	for _, tbl := range sm.GetTables() {
		// Group index column defs by index name
		groups := make(map[string][]*md.IndexDef)
		order := make([]string, 0)
		for _, idx := range tbl.GetIndexes() {
			if _, seen := groups[idx.IndexName]; !seen {
				order = append(order, idx.IndexName)
			}
			groups[idx.IndexName] = append(groups[idx.IndexName], idx)
		}
		// Sort columns within each index by ordinal position
		for _, idxs := range groups {
			sort.Slice(idxs, func(i, j int) bool {
				return idxs[i].OrdinalPosition < idxs[j].OrdinalPosition
			})
		}
		for _, name := range order {
			idxs := groups[name]
			sql, err := g.dialect.BuildCreateIndex(idxs, g.opts)
			if err != nil {
				return files, err
			}
			if sql == "" {
				continue
			}
			path, err := g.writeFile(tbl.TableSchema, name, "index", sql)
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
		sql, err := g.dialect.BuildCreateView(v, g.opts)
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
		sql, err := g.dialect.BuildCreateSequence(seq, g.opts)
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

// GeneratePackages generates CREATE PACKAGE DDL for all packages.
func (g *DDLGenerator) GeneratePackages(sm *md.SchemaModel, schema string) ([]string, error) {
	var files []string
	for _, pkg := range sm.GetPackages(schema) {
		sql, err := g.dialect.BuildCreatePackage(pkg, g.opts)
		if err != nil {
			return files, err
		}
		if sql == "" {
			continue
		}
		path, err := g.writeFile(pkg.PackageSchema, pkg.PackageName, "package", sql)
		if err != nil {
			return files, err
		}
		files = append(files, path)
	}
	return files, nil
}

// GeneratePackageBodies generates CREATE PACKAGE BODY DDL for all package bodies.
func (g *DDLGenerator) GeneratePackageBodies(sm *md.SchemaModel, schema string) ([]string, error) {
	var files []string
	for _, pkg := range sm.GetPackageBodies(schema) {
		sql, err := g.dialect.BuildCreatePackageBody(pkg, g.opts)
		if err != nil {
			return files, err
		}
		if sql == "" {
			continue
		}
		path, err := g.writeFile(pkg.PackageSchema, pkg.PackageName, "package_body", sql)
		if err != nil {
			return files, err
		}
		files = append(files, path)
	}
	return files, nil
}

// GenerateTriggers generates CREATE TRIGGER DDL for all triggers.
func (g *DDLGenerator) GenerateTriggers(sm *md.SchemaModel) ([]string, error) {
	var files []string
	for _, tbl := range sm.GetTables() {
		for _, trg := range sm.GetTriggers(tbl.TableSchema, tbl.TableName) {
			sql, err := g.dialect.BuildCreateTrigger(trg, g.opts)
			if err != nil {
				return files, err
			}
			if sql == "" {
				continue
			}
			path, err := g.writeFile(trg.TriggerSchema, trg.TriggerName, "trigger", sql)
			if err != nil {
				return files, err
			}
			files = append(files, path)
		}
	}
	return files, nil
}

// GenerateFunctions generates CREATE FUNCTION DDL for all functions in a schema.
func (g *DDLGenerator) GenerateFunctions(sm *md.SchemaModel, schema string) ([]string, error) {
	var files []string
	for _, fn := range sm.GetFunctions(schema) {
		sql, err := g.dialect.BuildCreateFunction(fn, g.opts)
		if err != nil {
			return files, err
		}
		if sql == "" {
			continue
		}
		path, err := g.writeFile(fn.FunctionSchema, fn.FunctionName, "function", sql)
		if err != nil {
			return files, err
		}
		files = append(files, path)
	}
	return files, nil
}

// GenerateMViews generates CREATE MATERIALIZED VIEW DDL for all materialized views.
func (g *DDLGenerator) GenerateMViews(sm *md.SchemaModel) ([]string, error) {
	var files []string
	for _, mv := range sm.GetMViews() {
		sql, err := g.dialect.BuildCreateMView(mv, g.opts)
		if err != nil {
			return files, err
		}
		if sql == "" {
			continue
		}
		path, err := g.writeFile(mv.MViewSchema, mv.MViewName, "mview", sql)
		if err != nil {
			return files, err
		}
		files = append(files, path)
	}
	return files, nil
}

// GenerateSynonyms generates CREATE SYNONYM DDL for all synonyms.
func (g *DDLGenerator) GenerateSynonyms(sm *md.SchemaModel, schema string) ([]string, error) {
	var files []string
	for _, syn := range sm.GetSynonyms(schema) {
		sql, err := g.dialect.BuildCreateSynonym(syn, g.opts)
		if err != nil {
			return files, err
		}
		if sql == "" {
			continue
		}
		path, err := g.writeFile(syn.SynonymSchema, syn.SynonymName, "synonym", sql)
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
