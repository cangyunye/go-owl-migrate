package oceanbase

import (
	"fmt"
	"strings"

	"github.com/cangyunye/go-owl-migrate/internal/dialect"
	mysql "github.com/cangyunye/go-owl-migrate/internal/dialect/mysql"
	oracle "github.com/cangyunye/go-owl-migrate/internal/dialect/oracle"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
)

// ── OceanBase MySQL 租户 ──
// 文件级组合复用 MySQL 方言，仅覆盖差异：
//   - TRUNCATE 事务安全（OB 特性，与标准 MySQL 不同）
//   - 支持 SEQUENCE（OB 扩展，MySQL 8.0 无原生 SEQUENCE）

type obMySQLTypeMapper struct{ mysql.MySQLTypeMapper }

func (m obMySQLTypeMapper) Name() string { return "oceanbase-mysql" }

type obMySQLFeatures struct{ mysql.MySQLFeatures }

func (obMySQLFeatures) TruncateIsTransactional() bool { return true } // ← OB 差异

type obMySQLDDLBuilder struct{ mysql.MySQLDDLBuilder }

func (b obMySQLDDLBuilder) BuildCreateSequence(seq *md.SequenceDef, opts dialect.BuildOptions) (string, error) {
	return fmt.Sprintf("CREATE SEQUENCE `%s`.`%s` START WITH %d INCREMENT BY %d MAXVALUE %d NOCYCLE CACHE %d",
		seq.SequenceSchema, seq.SequenceName, seq.StartValue, seq.IncrementBy, seq.MaxValue, seq.CacheSize), nil
}

type obMySQLDMLHelper struct{ mysql.MySQLDMLHelper }

func NewMySQL() dialect.Dialect {
	mysqlD := mysql.New()
	return dialect.Dialect{
		TypeMapper:       obMySQLTypeMapper{},
		IdentifierQuoter: mysqlD.IdentifierQuoter,
		Features:         obMySQLFeatures{},
		DDLBuilder:       obMySQLDDLBuilder{},
		DMLHelper:        obMySQLDMLHelper{},
	}
}

// ── OceanBase Oracle 租户 ──
// 文件级组合复用 Oracle 方言，仅覆盖差异：
//   - TRUNCATE 事务安全（OB 特性，与标准 Oracle 不同）

type obOracleTypeMapper struct{ oracle.OracleTypeMapper }

func (m obOracleTypeMapper) Name() string { return "oceanbase-oracle" }

type obOracleFeatures struct{ oracle.OracleFeatures }

func (obOracleFeatures) TruncateIsTransactional() bool { return true } // ← OB 差异

type obOracleDDLBuilder struct{ oracle.OracleDDLBuilder }

func (b obOracleDDLBuilder) BuildCreateIndex(idxs []*md.IndexDef, opts dialect.BuildOptions) (string, error) {
	// OceanBase Oracle mode does not support Bitmap indexes
	for _, idx := range idxs {
		if strings.ToUpper(idx.IndexType) == "BITMAP" {
			// Build column list for diagnostic comment
			cols := make([]string, len(idxs))
			for i, ix := range idxs {
				cols[i] = ix.ColumnName
			}
			return fmt.Sprintf("-- MANUAL: Bitmap index not supported in OceanBase Oracle; CREATE INDEX %s ON %s (%s)",
				idxs[0].IndexName, idxs[0].TableName, strings.Join(cols, ", ")), nil
		}
	}
	return b.OracleDDLBuilder.BuildCreateIndex(idxs, opts)
}

type obOracleDMLHelper struct{ oracle.OracleDMLHelper }

func NewOracle() dialect.Dialect {
	oracleD := oracle.New()
	return dialect.Dialect{
		TypeMapper:       obOracleTypeMapper{},
		IdentifierQuoter: oracleD.IdentifierQuoter,
		Features:         obOracleFeatures{},
		DDLBuilder:       obOracleDDLBuilder{},
		DMLHelper:        obOracleDMLHelper{},
	}
}
