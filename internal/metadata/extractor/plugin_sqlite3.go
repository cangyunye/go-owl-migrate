//go:build sqlite3

package extractor

func init() {
	Register(&SQLite3MetadataQuerier{})
}
