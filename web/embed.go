package web

import "embed"

//go:embed static/* docs/* docsite/*
var FS embed.FS
