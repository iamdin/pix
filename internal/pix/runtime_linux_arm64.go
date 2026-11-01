package pix

import "embed"

//go:embed runtime-data/linux-arm64
var runtimeData embed.FS

const runtimeDataDir = "runtime-data/linux-arm64"
