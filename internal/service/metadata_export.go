package service

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

// ── 元数据导出单一实现（P3）：13 个规范文件，列集对齐 docs/csv-format.md ──

type csvSpec struct {
	typ     md.ObjectType
	headers []string
	rows    func() [][]string
}

func intS(v int) string { return strconv.Itoa(v) }
func boolYN(b string) string {
	if b == "" {
		return ""
	}
	if b == "YES" || b == "NO" {
		return b
	}
	return b
}

// ExportMetadataFiles 把（已按范围/对象选择处理后的）模型按文件级对象选择导出为
// 规范 CSV。objects 为 nil 时导出全部 13 个文件；选中文件始终生成（含表头）。
// 返回生成文件绝对路径（按 AllObjectTypes 顺序）。
func ExportMetadataFiles(dir string, sm *md.SchemaModel, objects md.ObjectSet) ([]string, error) {
	selected := func(t md.ObjectType) bool { return objects == nil || objects.Contains(t) }

	tables := sm.GetTables()
	schemas := sm.Schemas()

	specs := []csvSpec{
		{md.ObjectType("tables"), []string{"TABLE_SCHEMA", "TABLE_NAME", "TABLE_TYPE", "TABLE_COMMENT",
			"ENGINE", "TABLESPACE", "PARTITIONED", "PARTITION_INFO", "ROW_FORMAT", "TEMPORARY",
			"CHARSET", "COLLATION", "OWNER"}, func() [][]string {
			var rows [][]string
			for _, t := range tables {
				rows = append(rows, []string{t.TableSchema, t.TableName, t.TableType, t.TableComment,
					t.Engine, t.Tablespace, boolYN(t.Partitioned), t.PartitionInfo, t.RowFormat,
					boolYN(t.Temporary), t.Charset, t.Collation, t.Owner})
			}
			return rows
		}},
		{md.ObjectType("columns"), []string{"TABLE_SCHEMA", "TABLE_NAME", "COLUMN_NAME", "ORDINAL_POSITION",
			"DATA_TYPE", "DATA_LENGTH", "DATA_PRECISION", "DATA_SCALE", "NULLABLE", "DEFAULT_VALUE",
			"COLUMN_COMMENT", "IS_IDENTITY", "IDENTITY_GENERATION", "IDENTITY_START", "IDENTITY_INCREMENT",
			"CHAR_USED", "HIDDEN_COLUMN", "VIRTUAL_EXPRESSION", "ENUM_VALUES", "CHARACTER_SET", "COLLATION", "ON_UPDATE"}, func() [][]string {
			var rows [][]string
			for _, t := range tables {
				for _, c := range t.GetColumns() {
					rows = append(rows, []string{c.TableSchema, c.TableName, c.ColumnName, intS(c.OrdinalPosition),
						c.DataType, intS(c.DataLength), intS(c.DataPrecision), intS(c.DataScale), c.Nullable,
						c.DefaultValue, c.ColumnComment, boolYN(c.IsIdentity), c.IdentityGeneration,
						intS(c.IdentityStart), intS(c.IdentityIncrement), c.CharUsed, boolYN(c.HiddenColumn),
						c.VirtualExpression, c.EnumValues, c.CharacterSet, c.Collation, c.OnUpdate})
				}
			}
			return rows
		}},
		{md.ObjectType("primary_keys"), []string{"TABLE_SCHEMA", "TABLE_NAME", "CONSTRAINT_NAME", "COLUMN_NAME", "ORDINAL_POSITION"}, func() [][]string {
			var rows [][]string
			for _, t := range tables {
				for _, p := range t.GetPrimaryKeys() {
					rows = append(rows, []string{p.TableSchema, p.TableName, p.ConstraintName, p.ColumnName, intS(p.OrdinalPosition)})
				}
			}
			return rows
		}},
		{md.ObjectType("indexes"), []string{"TABLE_SCHEMA", "TABLE_NAME", "INDEX_NAME", "INDEX_TYPE",
			"UNIQUENESS", "COLUMN_NAME", "ORDINAL_POSITION", "EXPRESSION", "DESCEND", "WHERE_CLAUSE"}, func() [][]string {
			var rows [][]string
			for _, t := range tables {
				for _, ix := range t.GetIndexes() {
					rows = append(rows, []string{ix.TableSchema, ix.TableName, ix.IndexName, ix.IndexType,
						ix.Uniqueness, ix.ColumnName, intS(ix.OrdinalPosition), ix.Expression, ix.Descend, ix.WhereClause})
				}
			}
			return rows
		}},
		{md.ObjectType("foreign_keys"), []string{"CONSTRAINT_NAME", "TABLE_SCHEMA", "TABLE_NAME", "COLUMN_NAME",
			"REF_SCHEMA", "REF_TABLE", "REF_COLUMN", "DELETE_RULE", "UPDATE_RULE", "DEFERRABLE"}, func() [][]string {
			var rows [][]string
			for _, t := range tables {
				for _, f := range t.GetForeignKeys() {
					rows = append(rows, []string{f.ConstraintName, f.TableSchema, f.TableName, f.ColumnName,
						f.RefSchema, f.RefTable, f.RefColumn, f.DeleteRule, f.UpdateRule, f.Deferrable})
				}
			}
			return rows
		}},
		{md.ObjectType("views"), []string{"VIEW_SCHEMA", "VIEW_NAME", "VIEW_DEFINITION", "VIEW_COMMENT"}, func() [][]string {
			var rows [][]string
			for _, v := range sm.Views {
				rows = append(rows, []string{v.ViewSchema, v.ViewName, v.ViewDefinition, v.ViewComment})
			}
			return rows
		}},
		{md.ObjectType("mviews"), []string{"MVIEW_SCHEMA", "MVIEW_NAME", "MVIEW_QUERY", "REFRESH_METHOD",
			"REFRESH_MODE", "REFRESH_INTERVAL", "BUILD_MODE", "MVIEW_COMMENT"}, func() [][]string {
			var rows [][]string
			for _, mv := range sm.GetMViews() {
				rows = append(rows, []string{mv.MViewSchema, mv.MViewName, mv.MViewQuery, mv.RefreshMethod,
					mv.RefreshMode, mv.RefreshInterval, mv.BuildMode, mv.MViewComment})
			}
			return rows
		}},
		{md.ObjectType("sequences"), []string{"SEQUENCE_SCHEMA", "SEQUENCE_NAME", "START_VALUE", "INCREMENT_BY",
			"MIN_VALUE", "MAX_VALUE", "CYCLE", "CACHE_SIZE", "ORDER_FLAG", "CURRENT_VALUE", "DATA_TYPE"}, func() [][]string {
			var rows [][]string
			for _, sch := range schemas {
				for _, s := range sm.GetSequences(sch) {
					rows = append(rows, []string{s.SequenceSchema, s.SequenceName, intS(s.StartValue), intS(s.IncrementBy),
						intS(s.MinValue), intS(s.MaxValue), boolYN(s.Cycle), intS(s.CacheSize), s.OrderFlag,
						intS(s.CurrentValue), s.DataType})
				}
			}
			return rows
		}},
		{md.ObjectType("synonyms"), []string{"SYNONYM_NAME", "SYNONYM_SCHEMA", "TARGET_SCHEMA", "TARGET_NAME",
			"IS_PUBLIC", "TARGET_TYPE"}, func() [][]string {
			var rows [][]string
			for _, s := range sm.Synonyms {
				rows = append(rows, []string{s.SynonymName, s.SynonymSchema, s.TargetSchema, s.TargetName, boolYN(s.IsPublic), s.TargetType})
			}
			return rows
		}},
		{md.ObjectType("triggers"), []string{"TRIGGER_SCHEMA", "TRIGGER_NAME", "TABLE_SCHEMA", "TABLE_NAME",
			"TRIGGER_TYPE", "TRIGGER_EVENT", "TRIGGER_BODY", "STATUS", "FOR_EACH", "WHEN_CLAUSE",
			"REFERENCING", "DESCRIPTION", "LANGUAGE"}, func() [][]string {
			var rows [][]string
			for _, t := range tables {
				for _, trg := range sm.GetTriggers(t.TableSchema, t.TableName) {
					rows = append(rows, []string{trg.TriggerSchema, trg.TriggerName, trg.TableSchema, trg.TableName,
						trg.TriggerType, trg.TriggerEvent, trg.TriggerBody, trg.Status, trg.ForEach,
						trg.WhenClause, trg.Referencing, trg.Description, trg.Language})
				}
			}
			return rows
		}},
		{md.ObjectType("functions"), []string{"FUNCTION_SCHEMA", "FUNCTION_NAME", "FUNCTION_TYPE", "RETURN_TYPE",
			"FUNCTION_BODY", "LANGUAGE", "STATUS", "ARGUMENTS", "AUTH_ID", "DETERMINISTIC", "PARALLEL"}, func() [][]string {
			var rows [][]string
			for _, sch := range schemas {
				for _, f := range sm.GetFunctions(sch) {
					rows = append(rows, []string{f.FunctionSchema, f.FunctionName, f.FunctionType, f.ReturnType,
						f.FunctionBody, f.Language, f.Status, f.Arguments, f.AuthID, f.Deterministic, f.Parallel})
				}
			}
			return rows
		}},
		{md.ObjectType("packages"), []string{"PACKAGE_SCHEMA", "PACKAGE_NAME", "PACKAGE_SPEC", "STATUS",
			"AUTH_ID", "DESCRIPTION"}, func() [][]string {
			var rows [][]string
			for _, sch := range schemas {
				for _, p := range sm.GetPackages(sch) {
					rows = append(rows, []string{p.PackageSchema, p.PackageName, p.PackageSpec, p.Status, p.AuthID, p.Description})
				}
			}
			return rows
		}},
		{md.ObjectType("package_bodies"), []string{"PACKAGE_SCHEMA", "PACKAGE_NAME", "PACKAGE_BODY", "STATUS"}, func() [][]string {
			var rows [][]string
			for _, sch := range schemas {
				for _, p := range sm.GetPackageBodies(sch) {
					rows = append(rows, []string{p.PackageSchema, p.PackageName, p.PackageBody, p.Status})
				}
			}
			return rows
		}},
	}

	var files []string
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	for _, sp := range specs {
		if !selected(sp.typ) {
			continue
		}
		path, err := writeMetaCSVFile(dir, string(sp.typ), sp.headers, sp.rows())
		if err != nil {
			return files, err
		}
		files = append(files, path)
	}
	return files, nil
}

func writeMetaCSVFile(dir, name string, headers []string, rows [][]string) (string, error) {
	path := filepath.Join(dir, name+".csv")
	f, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("create %s: %w", path, err)
	}
	defer f.Close()
	w := csv.NewWriter(f)
	if len(headers) > 0 {
		if err := w.Write(headers); err != nil {
			return "", err
		}
	}
	for _, row := range rows {
		if err := w.Write(row); err != nil {
			return "", err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return "", err
	}
	return path, nil
}
