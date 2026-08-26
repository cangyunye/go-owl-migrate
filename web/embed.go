package web

import "embed"

//go:embed templates/* static/* docs/* docsite/*
var FS embed.FS
