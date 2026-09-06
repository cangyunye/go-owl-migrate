//go:build e2e && ob

package e2eagent

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"

	"github.com/cangyunye/go-owl-migrate/internal/dbconn"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
	"github.com/cangyunye/go-owl-migrate/internal/transfer/exporter"
)

// ── 字符集：UTF8 租户上验证「服务端编码判断 + GBK 转换」（阶段二计划 §4.4）──
//
// OB GBK 租户因资源不足暂缓构建（待处理项）。在现有 UTF8 租户上先确认两件
// 事：① 探针能在 native 与 agent 两个通道上判得同一服务端字符集；
// ② GBK 属「出口转码」——进程内已是 UTF-8，出口写 GBK 由文件层完成，两通道
// 产出必须逐字节一致。OB GBK 租户到位后再补 S1 的真 GBK 入口场景。

func TestE2E_CharsetProbeUTF8Tenant(t *testing.T) {
	native, ag, _ := paritySource(t)

	en, err := dbconn.ProbeServerEncoding(context.Background(), native, "oceanbase-oracle")
	if err != nil {
		t.Fatalf("native probe: %v", err)
	}
	ea, err := dbconn.ProbeServerEncoding(context.Background(), ag, "oceanbase-oracle")
	if err != nil {
		t.Fatalf("agent probe: %v", err)
	}
	if en != ea {
		t.Fatalf("charset mismatch: native=%+v agent=%+v", en, ea)
	}
	if !en.Unicode {
		t.Fatalf("expected a Unicode server charset on the UTF8 tenant, got %+v", en)
	}
	t.Logf("server charset detected identically on both channels: %+v", en)
}

// TestE2E_CharsetGBKExport 在 UTF8 租户上写入含中文/生僻字/Latin-1 的数据，
// 分别经 native 与 agent 通道导出为 GBK 编码 CSV：两通道逐字节一致，且解码
// 回来与 UTF8 基线导出内容一致（出口转码与通道无关）。
func TestE2E_CharsetGBKExport(t *testing.T) {
	native, ag, schema := paritySource(t)

	const tbl = "OWL_CHARSET_GBK"
	tryExec(t, native, "DROP TABLE "+tbl)
	mustExec(t, native, `CREATE TABLE `+tbl+` (
		ID NUMBER(10) NOT NULL,
		TXT VARCHAR2(200),
		CONSTRAINT PK_`+tbl+` PRIMARY KEY (ID)
	)`)
	t.Cleanup(func() { tryExec(t, native, "DROP TABLE "+tbl) })

	// GBK 安全集：常用中文、全角标点、GBK 范围生僻字、全角数字/符号/罗马数字。
	// 注意：Latin-1 补充区（é 等）不在 GBK 编码表（x/text 报 rune not
	// supported），不可放进安全集；不可表示字符的失败语义见下方子测试。
	rows := []struct {
		id  int
		txt string
	}{
		{1, "中文常用字与全角标点：，！？"},
		{2, "GBK 范围生僻字：镕堃旻"},
		{3, "全角与符号：０１２３ ①⑵ Ⅷ"},
		{4, "拼音扩展区：ā á ǎ à é ê"},
	}
	for _, r := range rows {
		if _, err := native.ExecContext(context.Background(),
			"INSERT INTO "+tbl+" (ID, TXT) VALUES (?, ?)", r.id, r.txt); err != nil {
			t.Fatalf("seed row %d: %v", r.id, err)
		}
	}
	// OB-Oracle native 驱动不自动提交：不 COMMIT 的话数据停在 native 会话的
	// 未提交事务里，agent 会话看到的表是空的。
	mustExec(t, native, "COMMIT")
	// 诊断：两通道都必须看到刚提交的 4 行，排除会话可见性问题。
	for name, db := range map[string]*sql.DB{"native": native, "agent": ag} {
		var n int
		if err := db.QueryRowContext(context.Background(),
			"SELECT COUNT(*) FROM "+tbl).Scan(&n); err != nil {
			t.Fatalf("%s count: %v", name, err)
		}
		if n != len(rows) {
			t.Fatalf("%s sees %d rows, want %d", name, n, len(rows))
		}
	}

	tblDef := charsetTableDef(schema, tbl)
	pks := map[string][]string{schema + "." + tbl: {"ID"}}

	// expCfgWith returns an exporter config writing CSV in the given file
	// encoding ("" = UTF-8 baseline).
	expCfgWith := func(dir, encoding string) exporter.Config {
		return exporter.Config{
			OutputDir: dir, Format: "csv", CSVHeader: true,
			CSVDelimiter: ",", CSVNullRep: "\\N",
			PageSize: 500, MaxWorkers: 1, DBType: "oracle",
			PlaceholderFamily: "qmark", // native obconnector-go 走 "?" 绑定
			CSVEncoding:       encoding,
		}
	}

	dirUTF8 := t.TempDir()
	if _, err := exporter.New(native, expCfgWith(dirUTF8, "")).ExportTables(context.Background(),
		[]*md.TableDef{tblDef}, pks); err != nil {
		t.Fatalf("utf8 baseline export: %v", err)
	}

	dirN := t.TempDir()
	dirA := t.TempDir()
	resN, err := exporter.New(native, expCfgWith(dirN, "gbk")).ExportTables(context.Background(),
		[]*md.TableDef{tblDef}, pks)
	if err != nil {
		t.Fatalf("native gbk export: %v", err)
	}
	resA, err := exporter.New(ag, expCfgWith(dirA, "gbk")).ExportTables(context.Background(),
		[]*md.TableDef{tblDef}, pks)
	if err != nil {
		t.Fatalf("agent gbk export: %v", err)
	}
	// 单表错误只落在 result.Error 上，不会冒泡成顶层 error。
	for _, r := range append(resN, resA...) {
		if r.Error != nil {
			t.Fatalf("export %s.%s: %v", r.Schema, r.Table, r.Error)
		}
		if r.Rows != int64(len(rows)) {
			t.Fatalf("export %s.%s rows = %d, want %d", r.Schema, r.Table, r.Rows, len(rows))
		}
	}

	filesN := csvMap(t, dirN)
	filesA := csvMap(t, dirA)
	if len(filesN) != len(filesA) {
		t.Fatalf("gbk csv file count native=%d agent=%d", len(filesN), len(filesA))
	}
	dec := simplifiedchinese.GBK.NewDecoder()
	for name, bn := range filesN {
		ba, ok := filesA[name]
		if !ok {
			t.Fatalf("missing agent gbk csv for %s", name)
		}
		if !bytes.Equal(bn, ba) {
			t.Fatalf("gbk csv bytes differ between channels: %s (native %d B, agent %d B)", name, len(bn), len(ba))
		}
		// GBK 文件解码回 UTF-8 后必须与 UTF8 基线导出逐字节一致。
		utf8Path := filepath.Join(dirUTF8, name)
		baseline, err := os.ReadFile(utf8Path)
		if err != nil {
			t.Fatalf("read baseline %s: %v", utf8Path, err)
		}
		decoded, err := dec.Bytes(bn)
		if err != nil {
			t.Fatalf("gbk decode %s: %v", name, err)
		}
		if !bytes.Equal(decoded, baseline) {
			t.Fatalf("gbk round-trip differs from utf8 baseline: %s\nDecoded: %q\nBaseline: %q", name, decoded, baseline)
		}
		for _, r := range rows {
			if r.txt != "" && !strings.Contains(string(decoded), r.txt) {
				t.Fatalf("decoded csv %s lost content %q", name, r.txt)
			}
		}
	}
	t.Logf("charset gbk export ok: %d csv files byte-identical across channels and round-trip to the utf8 baseline", len(filesN))
}

// TestE2E_CharsetGBKUnsupportedRune 断言不可表示字符（é 等 Latin-1 补充区，
// 不在 GBK 编码表）的失败语义：文件层编码器报错，native 与 agent 行为一致
// （错误信息同一来源，且都必须失败——不允许静默替换）。
func TestE2E_CharsetGBKUnsupportedRune(t *testing.T) {
	native, ag, schema := paritySource(t)

	const tbl = "OWL_CHARSET_GBK_UNSUP"
	tryExec(t, native, "DROP TABLE "+tbl)
	mustExec(t, native, `CREATE TABLE `+tbl+` (
		ID NUMBER(10) NOT NULL,
		TXT VARCHAR2(200),
		CONSTRAINT PK_`+tbl+` PRIMARY KEY (ID)
	)`)
	t.Cleanup(func() { tryExec(t, native, "DROP TABLE "+tbl) })
	if _, err := native.ExecContext(context.Background(),
		"INSERT INTO "+tbl+" (ID, TXT) VALUES (?, ?)", 1, "café naïve"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	mustExec(t, native, "COMMIT")

	tblDef := charsetTableDef(schema, tbl)
	pks := map[string][]string{schema + "." + tbl: {"ID"}}
	for name, db := range map[string]*sql.DB{"native": native, "agent": ag} {
		res, err := exporter.New(db, exporter.Config{
			OutputDir: t.TempDir(), Format: "csv", CSVHeader: true,
			CSVDelimiter: ",", PageSize: 500, MaxWorkers: 1,
			DBType: "oracle", PlaceholderFamily: "qmark",
			CSVEncoding: "gbk",
		}).ExportTables(context.Background(), []*md.TableDef{tblDef}, pks)
		if err != nil {
			t.Fatalf("%s export: %v", name, err)
		}
		if len(res) != 1 || res[0].Error == nil || !strings.Contains(res[0].Error.Error(), "rune not supported") {
			t.Fatalf("%s: want rune-not-supported error, got %+v", name, res)
		}
	}
}

func charsetTableDef(schema, table string) *md.TableDef {
	tbl, err := md.NewTableDef(schema, table)
	if err != nil {
		panic(err)
	}
	cols := []struct {
		name     string
		typ      string
		prec     int
		nullable string
	}{
		{"ID", "NUMBER", 10, "NO"},
		{"TXT", "VARCHAR2", 200, "YES"},
	}
	for i, c := range cols {
		cd, err := md.NewColumnDef(schema, table, c.name, i+1, c.typ)
		if err != nil {
			panic(err)
		}
		cd.DataPrecision = c.prec
		cd.Nullable = c.nullable
		if err := tbl.AddColumn(cd); err != nil {
			panic(err)
		}
	}
	tbl.AddPrimaryKey("PK_"+table, "ID")
	return tbl
}
