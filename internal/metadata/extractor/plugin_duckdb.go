//go:build duckdb

package extractor

func init() {
	Register(&DuckDBMetadataQuerier{})
}
