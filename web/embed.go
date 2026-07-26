package web

import "embed"

//go:embed templates/* static/* docs/*
var FS embed.FS
