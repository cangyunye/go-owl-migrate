package service

import (
	"fmt"
	"strings"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/dialect"
	"github.com/cangyunye/go-owl-migrate/internal/generator"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
	"github.com/cangyunye/go-owl-migrate/internal/registry"
)

// ── 类型边界（源方言元数据 → 目标方言 DDL），从 cmd/tableddl.go 迁入 ──

// TargetTypeFamily classifies a database type into its SQL type family
// (postgres/mysql/oracle/…), used to decide cross-dialect type conversion.
func TargetTypeFamily(dbType string) string {
	t := strings.ToLower(strings.TrimSpace(dbType))
	t = registry.Normalize(t)
	switch {
	case t == "panweidb" || strings.HasPrefix(t, "panweidb-") ||
			t == "opengaussdb" || strings.HasPrefix(t, "opengaussdb-"):
		// PanWeiDB / openGauss speak the PostgreSQL wire protocol in all SQL modes.
		return "postgres"
	case t == "mysql" || strings.HasSuffix(t, "-mysql"):
		return "mysql"
	case t == "oracle" || strings.HasSuffix(t, "-oracle"):
		return "oracle"
	case t == "sqlite3" || t == "duckdb":
		return t
	default:
		return "postgres"
	}
}

// ResolveSourceDialect determines the dialect of the metadata column types:
// explicit ddl.source_dialect wins, then the live source database type.
func ResolveSourceDialect(cfg *config.Config) string {
	if s := strings.TrimSpace(cfg.DDL.SourceDialect); s != "" {
		return s
	}
	return strings.TrimSpace(cfg.Source.Type)
}

// ConvertTableTypes returns a copy of tbl with column types converted from the
// source dialect to the target dialect via the LogicalType IR.
func ConvertTableTypes(tbl *md.TableDef, src, tgt dialect.Dialect, opts dialect.BuildOptions) *md.TableDef {
	cols := tbl.GetColumns()
	newCols := make([]*md.ColumnDef, len(cols))
	for i, col := range cols {
		nc := *col
		nc.DataType = convertColumnType(col, src, tgt, opts)
		// Source-dialect DEFAULT expressions (e.g. nextval(...), sysdate) are
		// not portable across dialects; drop them on cross-dialect conversion.
		nc.DefaultValue = ""
		newCols[i] = &nc
	}
	cp := *tbl
	cp.Columns = newCols
	return &cp
}

func convertColumnType(col *md.ColumnDef, src, tgt dialect.Dialect, opts dialect.BuildOptions) string {
	if override, ok := dialect.ApplyTypeOverride(col.DataType, col.DataLength, col.DataPrecision, col.DataScale, opts); ok {
		return override
	}
	lt := src.ToLogicalType(col.DataType, col.DataLength, col.DataPrecision, col.DataScale)
	return tgt.FromLogicalType(lt)
}

// QualifyTableTypes returns a copy of tbl with length/precision qualifiers
// appended to bare column types (CSV-style metadata keeps them in separate
// fields).
func QualifyTableTypes(tbl *md.TableDef, opts dialect.BuildOptions) *md.TableDef {
	cols := tbl.GetColumns()
	newCols := make([]*md.ColumnDef, len(cols))
	for i, col := range cols {
		nc := *col
		nc.DataType = qualifyColumnType(col, opts)
		newCols[i] = &nc
	}
	cp := *tbl
	cp.Columns = newCols
	return &cp
}

// ConvertSchemaModelForDDL returns a copy of sm whose table columns are
// qualified (same type family) or converted (cross family) for the target
// dialect. Live-extracted metadata carries bare data_type values (e.g.
// "varchar", "decimal") with length/precision in separate fields, so without
// this the generator would emit invalid target DDL. Returns the input model
// unchanged when no source dialect can be resolved.
func ConvertSchemaModelForDDL(sm *md.SchemaModel, cfg *config.Config, tgt dialect.Dialect, opts dialect.BuildOptions) *md.SchemaModel {
	srcName := ResolveSourceDialect(cfg)
	if srcName == "" {
		return sm
	}
	srcNorm := registry.Normalize(strings.ToLower(srcName))
	tgtNorm := registry.Normalize(strings.ToLower(cfg.DDL.TargetDialect))
	// Same type family (e.g. oceanbase-mysql → mysql) keeps source types and
	// DEFAULTs verbatim, only adding length/precision qualifiers; the LogicalType
	// IR conversion is reserved for genuinely different families (mysql ↔ pg ↔
	// oracle) where defaults are not portable anyway.
	cross := srcNorm != tgtNorm && TargetTypeFamily(srcNorm) != TargetTypeFamily(tgtNorm)

	var src dialect.Dialect
	if cross {
		s, err := registry.Get(srcNorm)
		if err != nil {
			return sm
		}
		src = s
	}

	converted := md.NewSchemaModel()
	for _, tbl := range sm.GetTables() {
		var out *md.TableDef
		if cross {
			out = ConvertTableTypes(tbl, src, tgt, opts)
		} else {
			out = QualifyTableTypes(tbl, opts)
		}
		_ = converted.AddTable(out) // GetTables() yields unique keys
	}
	// 只转换表列类型；其余对象（独立对象 + 触发器）原样保留，供生成侧消费。
	for _, v := range sm.Views {
		converted.AddView(v)
	}
	for _, mv := range sm.GetMViews() {
		converted.AddMView(mv)
	}
	for _, syn := range sm.Synonyms {
		converted.AddSynonym(syn)
	}
	for _, sch := range sm.Schemas() {
		for _, seq := range sm.GetSequences(sch) {
			converted.AddSequence(seq)
		}
		for _, fn := range sm.GetFunctions(sch) {
			converted.AddFunction(fn)
		}
		for _, pkg := range sm.GetPackages(sch) {
			converted.AddPackage(pkg)
		}
		for _, pkg := range sm.GetPackageBodies(sch) {
			converted.AddPackageBody(pkg)
		}
	}
	for _, tbl := range sm.GetTables() {
		for _, trg := range sm.GetTriggers(tbl.TableSchema, tbl.TableName) {
			converted.AddTrigger(trg)
		}
	}
	return converted
}

// BuildCreateTableViaDialect renders the CREATE TABLE statement for the target
// database using the dialect system. Cross-dialect type conversion goes through
// the LogicalType IR; otherwise source types are emitted with qualifiers.
func BuildCreateTableViaDialect(tbl *md.TableDef, cfg *config.Config) (string, error) {
	targetName := registry.Normalize(strings.ToLower(cfg.Target.Type))
	target, err := registry.Get(targetName)
	if err != nil {
		return "", fmt.Errorf("unknown target dialect %q: %w", cfg.Target.Type, err)
	}

	opts := ToBuildOptions(cfg)
	opts.TargetDialect = targetName
	opts.PreserveIdentifierCase = true

	converted := tbl
	if srcName := ResolveSourceDialect(cfg); srcName != "" {
		srcNorm := registry.Normalize(strings.ToLower(srcName))
		if srcNorm != targetName && TargetTypeFamily(srcNorm) != TargetTypeFamily(targetName) {
			if src, serr := registry.Get(srcNorm); serr == nil {
				converted = ConvertTableTypes(tbl, src, target, opts)
			} else {
				converted = QualifyTableTypes(tbl, opts)
			}
		} else {
			converted = QualifyTableTypes(tbl, opts)
		}
	} else {
		converted = QualifyTableTypes(tbl, opts)
	}

	return target.BuildCreateTable(converted, opts)
}

func qualifyColumnType(col *md.ColumnDef, opts dialect.BuildOptions) string {
	if _, ok := dialect.ApplyTypeOverride(col.DataType, col.DataLength, col.DataPrecision, col.DataScale, opts); ok {
		return col.DataType
	}
	dt := strings.TrimSpace(col.DataType)
	if strings.Contains(dt, "(") {
		return dt
	}
	up := strings.ToUpper(dt)
	switch {
	case isCharTypeName(up) && col.DataLength > 0:
		return fmt.Sprintf("%s(%d)", dt, col.DataLength)
	case isNumericTypeName(up) && col.DataPrecision > 0:
		if col.DataScale > 0 {
			return fmt.Sprintf("%s(%d,%d)", dt, col.DataPrecision, col.DataScale)
		}
		return fmt.Sprintf("%s(%d)", dt, col.DataPrecision)
	default:
		return dt
	}
}

func isCharTypeName(t string) bool {
	switch t {
	case "VARCHAR", "VARCHAR2", "NVARCHAR2", "CHAR", "NCHAR", "CHARACTER",
		"CHARACTER VARYING", "RAW", "VARBINARY", "BINARY", "STRING":
		return true
	}
	return false
}

func isNumericTypeName(t string) bool {
	switch t {
	case "NUMBER", "NUMERIC", "DECIMAL", "DEC":
		return true
	}
	return false
}

// ── GenerateDDL：CLI export ddl 与 serve /ddl/generate 的单一实现 ──

// GenerateDDL 从 SchemaModel 生成目标方言 DDL 文件到 outDir，返回文件列表。
//   - include：表清单模式（nil/空 = 全部）；大小写不敏感、支持 glob（ADR-003）；
//     过滤表及其附随对象；视图/物化视图收窄到选中表所属 owner（与其他 schema 级对象
//     按 owner 分组生成一致，避免单表请求带出无关 owner 的对象）。
//   - noQuote：非 nil 时覆盖配置的 no_quote_identifiers。
//   - 列类型按源→目标方言在 LogicalType 边界转换（跨类型族）或补限定（同族）。
//   - 序列/同义词/函数/包/包体按模型实际 owner 分组生成（多 owner 正确性）。
func GenerateDDL(sm *md.SchemaModel, cfg *config.Config, include []string, noQuote *bool, outDir string) ([]string, error) {
	d, err := registry.Get(cfg.DDL.TargetDialect)
	if err != nil {
		return nil, fmt.Errorf("unknown target dialect: %w", err)
	}

	opts := ToBuildOptions(cfg)
	if noQuote != nil {
		opts.NoQuoteIdentifiers = *noQuote
	}

	if len(include) > 0 {
		sm = filterSchemaTables(sm, include)
	}
	sm = ConvertSchemaModelForDDL(sm, cfg, d, opts)

	gen := generator.NewDDLGenerator(d, opts, outDir)
	var all []string
	collect := func(files []string, e error) error {
		if e != nil {
			return e
		}
		all = append(all, files...)
		return nil
	}

	for _, step := range []func() ([]string, error){
		func() ([]string, error) { return gen.GenerateTables(sm) },
		func() ([]string, error) { return gen.GenerateIndexes(sm) },
		func() ([]string, error) { return gen.GenerateViews(sm) },
		func() ([]string, error) { return gen.GenerateMViews(sm) },
		func() ([]string, error) { return gen.GenerateTriggers(sm) },
	} {
		if err := collect(step()); err != nil {
			return all, err
		}
	}
	// schema 级对象按模型实际 owner 分组
	for _, sch := range sm.Schemas() {
		for _, step := range []func() ([]string, error){
			func() ([]string, error) { return gen.GenerateSequences(sm, sch) },
			func() ([]string, error) { return gen.GenerateSynonyms(sm, sch) },
			func() ([]string, error) { return gen.GenerateFunctions(sm, sch) },
			func() ([]string, error) { return gen.GeneratePackages(sm, sch) },
			func() ([]string, error) { return gen.GeneratePackageBodies(sm, sch) },
		} {
			if err := collect(step()); err != nil {
				return all, err
			}
		}
	}
	return all, nil
}

// filterSchemaTables returns a shallow copy keeping only matched tables.
// 附随对象随表指针保留；视图/物化视图按"选中表所属 owner"收窄（与序列/函数等
// schema 级对象按 owner 分组生成一致），不再整模型泄漏（R2：单表输入带出无关对象）。
func filterSchemaTables(sm *md.SchemaModel, include []string) *md.SchemaModel {
	sel := md.SelectorFromInclude(include)
	out := *sm
	out.Tables = make(map[string]*md.TableDef, len(sm.Tables))
	for key, t := range sm.Tables {
		if sel.Matches(t.TableSchema, t.TableName) {
			out.Tables[key] = t
		}
	}
	owners := make(map[string]bool, len(out.Tables))
	for _, t := range out.Tables {
		owners[t.TableSchema] = true
	}
	viewKeep := make([]*md.ViewDef, 0, len(sm.Views))
	for _, v := range sm.Views {
		if owners[v.ViewSchema] {
			viewKeep = append(viewKeep, v)
		}
	}
	out.Views = viewKeep
	mvKeep := make([]*md.MViewDef, 0, len(sm.MViews))
	for _, mv := range sm.MViews {
		if owners[mv.MViewSchema] {
			mvKeep = append(mvKeep, mv)
		}
	}
	out.MViews = mvKeep
	return &out
}
