---
name: database-metadata-extractor
description: Implemented metadata/extractor package for live DB schema introspection across PG/MySQL/Oracle
metadata:
  type: project
---

Implemented extractor package at `internal/metadata/extractor/` for connecting to live databases and extracting full schema metadata (tables, columns, PKs, indexes, FKs, views, sequences, triggers) into the unified `*md.SchemaModel`.

**Why:** The project previously only supported CSV metadata files for DDL generation. Structure-based migration requires live database connection. Without this, migration from a source to a target was impossible without separately pre-generating CSV metadata files.

**How to apply:** `metadata.type: database` in YAML config triggers live DB extraction; `metadata.type: csv` preserves the existing behavior. The `loadSchemaModel(cfg)` function in `internal/cmd/metadata.go` dispatches between the two paths. See `internal/metadata/extractor/extractor.go` for the `MetadataQuerier` interface and registration pattern.

Key design: Postgres uses `information_schema` ($1 placeholders), MySQL uses `information_schema` (? placeholders), Oracle uses `ALL_*` dictionary views (:1 placeholders per go-ora driver). Downstream generators are unaware of the metadata source.
