package pitch

import "embed"

//go:embed runtime-data/linux-amd64
var runtimeData embed.FS

const runtimeDataDir = "runtime-data/linux-amd64"
