package dashboard

import "embed"

//go:embed assets/*
var assetsFS embed.FS

//go:embed templates/*
var templatesFS embed.FS
