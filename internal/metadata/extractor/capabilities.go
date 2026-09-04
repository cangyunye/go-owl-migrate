package extractor

import (
	"strings"

	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

// Capabilities 返回某源方言 querier 原生支持的对象类型集合（ADR-004：能力∩选择）。
// 按 base 归一（mysql/oracle/postgres）+ 变体差异：
//   - oracle 家族：全部对象（同义词/包/包体仅 oracle）
//   - postgres：无同义词/包/包体
//   - mysql 家族：无序列/物化视图/同义词/包/包体（有函数/触发器）
//   - OceanBase MySQL 租户（4.x）原生支持 SEQUENCE：已实测 CREATE SEQUENCE /
//     NEXTVAL / oceanbase.__all_sequence_object 字典可用（HANDOFF §三.2 P6-2）。
func Capabilities(dbType string) md.ObjectSet {
	base := normalizeDBType(dbType)
	out := md.ObjectSet{}
	add := func(names ...string) {
		for _, n := range names {
			out[md.ObjectType(n)] = true
		}
	}
	common := []string{"tables", "columns", "primary_keys", "indexes", "foreign_keys", "triggers", "views", "functions"}
	add(common...)
	switch base {
	case "oracle":
		add("mviews", "sequences", "synonyms", "packages", "package_bodies")
	case "postgres":
		add("mviews", "sequences")
	}
	if strings.EqualFold(dbType, "oceanbase-mysql") {
		add("sequences")
	}
	return out
}
