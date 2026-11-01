package pix

import "embed"

//go:embed runtime-data/darwin-arm64
var runtimeData embed.FS

const runtimeDataDir = "runtime-data/darwin-arm64"
