package metadata

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ── 对象类型（文件词干枚举，ADR-005）──

// ObjectType 是元数据对象类型，值为导出文件词干（tables / columns / ... / package_bodies）。
type ObjectType string

// 附随对象类型（ADR-002）：随所属表自动连带，不可单独挑选（导出文件层可拆，见 ADR-005）。
var attachedObjectTypes = map[ObjectType]bool{
	"columns":      true,
	"primary_keys": true,
	"indexes":      true,
	"foreign_keys": true,
	"triggers":     true,
}

// AllObjectTypes 返回全部 13 个对象类型（固定顺序：表、附随、独立）。
func AllObjectTypes() []ObjectType {
	return []ObjectType{
		"tables", "columns", "primary_keys", "indexes", "foreign_keys",
		"views", "mviews", "sequences", "synonyms", "triggers", "functions",
		"packages", "package_bodies",
	}
}

// IsAttached 报告对象类型是否附随于表（列/主键/索引/外键/触发器）。
func IsAttached(t ObjectType) bool {
	return attachedObjectTypes[t]
}

// ObjectSet 是一组对象类型。
type ObjectSet map[ObjectType]bool

// Contains 报告集合是否含 t。
func (s ObjectSet) Contains(t ObjectType) bool {
	return s[t]
}

// ParseObjectTypes 解析逗号分隔的对象类型串（大小写不敏感、去重、未知词报错）。
// 空串返回空集（由调用方决定默认语义：导出=方言全集，生成=仅表含附随）。
func ParseObjectTypes(s string) (ObjectSet, error) {
	known := make(map[string]ObjectType, len(AllObjectTypes()))
	for _, o := range AllObjectTypes() {
		known[strings.ToLower(string(o))] = o
	}
	out := make(ObjectSet)
	for _, part := range strings.Split(s, ",") {
		p := strings.ToLower(strings.TrimSpace(part))
		if p == "" {
			continue
		}
		o, ok := known[p]
		if !ok {
			return nil, fmt.Errorf("unknown object type %q (supported: %s)", part, strings.Join(objectTypeNames(AllObjectTypes()), ","))
		}
		out[o] = true
	}
	return out, nil
}

func objectTypeNames(ts []ObjectType) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = string(t)
	}
	return out
}

// ── 范围（schema × 表模式）──

// SchemaPattern 限定一个 schema 下按表名模式（glob，空 = 全部）选取的表。
// Schema 为空表示任意 owner。
// 语法见 ParseScope。
// ponytail: 存原始大小写，大小写不敏感在匹配时统一 fold（slice 3）。
type SchemaPattern struct {
	Schema       string // 空 = 任意 owner
	TablePattern string // glob，空 = 该 schema 全部表
}

// ParseScope 解析选择范围：
//
//	(空 | all)                     整个元数据（任意 schema、任意表）
//	schema:A,B                    schema A、B 的全部表
//	table:G1,G2                   任意 schema 下按表名 glob G1、G2 匹配的表
//	schema:A,B:table:G1,G2        schema A、B 分别按 glob G1、G2 匹配
//
// 兼容旧 CLI/UI 的 schema:NAME 与 table:T1,T2 写法（size 2 前缀）。
func ParseScope(s string) ([]SchemaPattern, error) {
	in := strings.TrimSpace(s)
	if in == "" || strings.EqualFold(in, "all") {
		return []SchemaPattern{{}}, nil
	}
	switch {
	case strings.HasPrefix(in, "schema:"):
		rest := strings.TrimPrefix(in, "schema:")
		schemasPart := rest
		tablesPart := ""
		if i := strings.Index(rest, ":table:"); i >= 0 {
			schemasPart = rest[:i]
			tablesPart = rest[i+len(":table:"):]
		}
		return crossScope(splitList(schemasPart), splitList(tablesPart)), nil
	case strings.HasPrefix(in, "table:"):
		return crossScope(nil, splitList(strings.TrimPrefix(in, "table:"))), nil
	default:
		return nil, fmt.Errorf("invalid scope %q: use all, schema:NAME[,NAME...], table:GLOB[,GLOB...], or schema:NAME[:table:GLOB]", in)
	}
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// crossScope 做 schema × 表的笛卡尔积：schemas 为空 = 任意 owner（单个空 schema），
// patterns 为空 = 该 schema 全部表（单个空模式）。
func crossScope(schemas, patterns []string) []SchemaPattern {
	if len(schemas) == 0 {
		schemas = []string{""}
	}
	if len(patterns) == 0 {
		patterns = []string{""}
	}
	out := make([]SchemaPattern, 0, len(schemas)*len(patterns))
	for _, s := range schemas {
		for _, p := range patterns {
			out = append(out, SchemaPattern{Schema: s, TablePattern: p})
		}
	}
	return out
}

// ── 排除规则（来自 config.table_filter.exclude，请求层不带）──

// ExcludeFilter 表达基线排除：glob / regex / 整 schema / 精确表。
// 与 config.TableFilterConfig.Exclude 同构，定义在 metadata 以避免 import 环。
type ExcludeFilter struct {
	Glob    []string
	Regex   []string
	Schemas []string
	Tables  []string // 精确表，schema.table 或裸表名
}

// ObjectSelector 是一次选择：范围（schema × 表模式）+ 排除（config 基线）。
// 大小写不敏感；优先级：显式点名精确表 > exclude > glob include（ADR-003）。
type ObjectSelector struct {
	Schemas []SchemaPattern
	Exclude ExcludeFilter
}

// Matches 报告 schema.table 是否被选中。
func (s ObjectSelector) Matches(schema, table string) bool {
	patterns := s.Schemas
	if len(patterns) == 0 {
		patterns = []SchemaPattern{{}}
	}
	for _, p := range patterns {
		if p.Schema != "" && !foldEq(p.Schema, schema) {
			continue
		}
		tp := p.TablePattern
		if tp == "" {
			// schema 全域：属于"全量"性质，受 exclude 约束
			if !s.Exclude.excluded(schema, table) {
				return true
			}
			continue
		}
		if isGlob(tp) {
			// glob include：受 exclude 约束
			if (globMatch(tp, table) || globMatch(tp, schema+"."+table)) && !s.Exclude.excluded(schema, table) {
				return true
			}
			continue
		}
		// 显式点名精确名：优先于 exclude
		if foldEq(tp, table) || foldEq(tp, schema+"."+table) {
			return true
		}
	}
	return false
}

func (s ExcludeFilter) excluded(schema, table string) bool {
	lt := strings.ToLower(table)
	lfull := strings.ToLower(schema + "." + table)
	for _, g := range s.Glob {
		if globMatch(g, table) || globMatch(g, schema+"."+table) {
			return true
		}
	}
	for _, r := range s.Regex {
		re, err := regexp.Compile(r)
		if err != nil {
			continue
		}
		if re.MatchString(table) || re.MatchString(schema+"."+table) ||
			re.MatchString(lt) || re.MatchString(lfull) {
			return true
		}
	}
	for _, sch := range s.Schemas {
		if foldEq(sch, schema) {
			return true
		}
	}
	for _, t := range s.Tables {
		if foldEq(t, table) || foldEq(t, schema+"."+table) {
			return true
		}
	}
	return false
}

func foldEq(a, b string) bool { return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b)) }

func isGlob(p string) bool { return strings.ContainsAny(p, "*?[") }

func globMatch(pattern, name string) bool {
	ok, _ := filepath.Match(strings.ToLower(pattern), strings.ToLower(name))
	return ok
}

// ── 选择到模型视图（ADR-002）──

// wholeSchemas 判定：哪些 schema 处于"整 schema"范围（TablePattern 为空）。
func (s ObjectSelector) wholeSchemas() (all bool, bySchema map[string]bool) {
	patterns := s.Schemas
	if len(patterns) == 0 {
		patterns = []SchemaPattern{{}}
	}
	bySchema = make(map[string]bool)
	for _, p := range patterns {
		if p.TablePattern != "" {
			continue
		}
		if p.Schema == "" {
			all = true
		} else {
			bySchema[strings.ToLower(p.Schema)] = true
		}
	}
	return all, bySchema
}

// Select 返回按范围过滤后的 SchemaModel 视图（浅拷贝，原模型不变）：
//   - 命中的表及其附随对象（列/主键/索引/外键/触发器）随表保留；
//   - 独立对象（视图/物化视图/序列/同义词/函数/包/包体）仅当所在 schema
//     处于"整 schema"范围时保留（不做依赖上卷）；
//   - 模型非空但零命中时返回错误并附相近名建议（ADR-004）。
func (s ObjectSelector) Select(sm *SchemaModel) (*SchemaModel, error) {
	wholeAll, wholeBySchema := s.wholeSchemas()
	schemaOK := func(sch string) bool {
		return wholeAll || wholeBySchema[strings.ToLower(sch)]
	}

	out := NewSchemaModel()
	for _, t := range sm.GetTables() {
		if s.Matches(t.TableSchema, t.TableName) {
			if err := out.AddTable(t); err != nil {
				return nil, err
			}
		}
	}

	// 触发器附随于表：只保留命中表上的
	for _, tbl := range out.GetTables() {
		for _, trg := range sm.GetTriggers(tbl.TableSchema, tbl.TableName) {
			out.AddTrigger(trg)
		}
	}

	// 独立对象：所在 schema 处于整 schema 范围才保留
	for _, v := range sm.Views {
		if schemaOK(v.ViewSchema) {
			out.AddView(v)
		}
	}
	for _, mv := range sm.MViews {
		if schemaOK(mv.MViewSchema) {
			out.AddMView(mv)
		}
	}
	for _, syn := range sm.Synonyms {
		if schemaOK(syn.SynonymSchema) {
			out.AddSynonym(syn)
		}
	}
	for _, seq := range sm.allSequences {
		if schemaOK(seq.SequenceSchema) {
			out.AddSequence(seq)
		}
	}
	for _, fn := range sm.allFunctions {
		if schemaOK(fn.FunctionSchema) {
			out.AddFunction(fn)
		}
	}
	for _, pkg := range sm.allPackages {
		if schemaOK(pkg.PackageSchema) {
			out.AddPackage(pkg)
		}
	}
	for _, pkg := range sm.allPackageBodies {
		if schemaOK(pkg.PackageSchema) {
			out.AddPackageBody(pkg)
		}
	}

	if sm.tableCount() > 0 && out.tableCount() == 0 && len(out.Views) == 0 {
		return nil, s.zeroHitError(sm)
	}
	return out, nil
}

func (sm *SchemaModel) tableCount() int { return len(sm.Tables) }

// zeroHitError 在零命中时给出可操作错误：可用 schema 清单 + 相近表名建议。
func (s ObjectSelector) zeroHitError(sm *SchemaModel) error {
	schemas := make(map[string]bool)
	var all []string
	for _, t := range sm.GetTables() {
		schemas[t.TableSchema] = true
		all = append(all, t.TableSchema+"."+t.TableName)
	}
	var schemaList []string
	for sch := range schemas {
		schemaList = append(schemaList, sch)
	}
	sort.Strings(schemaList)

	// 相近名：对每个表模式找前缀包含命中的全名（fold）
	seen := make(map[string]bool)
	var hints []string
	for _, p := range s.Schemas {
		if p.TablePattern == "" || isGlob(p.TablePattern) {
			continue
		}
		w := strings.ToLower(p.TablePattern)
		for _, full := range all {
			lf := strings.ToLower(full)
			if strings.Contains(lf, w) && !seen[lf] {
				seen[lf] = true
				hints = append(hints, full)
			}
		}
	}
	if len(hints) > 5 {
		hints = hints[:5]
	}
	msg := fmt.Sprintf("no tables matched the selection; available schemas: %s", strings.Join(schemaList, ", "))
	if len(hints) > 0 {
		msg += fmt.Sprintf("; did you mean: %s", strings.Join(hints, ", "))
	}
	return fmt.Errorf("%s", msg)
}

// Schemas 返回模型中出现过的 owner/schema 列表（按原样大小写，排序去重）。
func (sm *SchemaModel) Schemas() []string {
	seen := make(map[string]bool)
	var out []string
	for _, t := range sm.GetTables() {
		if !seen[t.TableSchema] {
			seen[t.TableSchema] = true
			out = append(out, t.TableSchema)
		}
	}
	sort.Strings(out)
	return out
}
