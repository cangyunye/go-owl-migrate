//go:build ob

package extractor

func init() {
	Register(OceanBaseOracleWireQuerier{OracleMetadataQuerier{Placeholder: "?", OceanBase: true}})
	Register(&OceanBaseMySQLQuerier{})
}
