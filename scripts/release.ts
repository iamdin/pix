/**
 * Build pitch for macOS ARM64 into dist-release/, the asset install.sh downloads; --publish also creates the GitHub
 * release v<Version>.
 *
 *   bun scripts/release.ts [--publish]
 *
 * Built locally: the embedded Bun canary archive (internal/pitch/runtime-data/bun.zip) is not in git, and canary
 * downloads expire.
 */
import fs from "node:fs";
import path from "node:path";

const root = path.resolve(import.meta.dir, "..");
const version = /const Version = "(.+)"/.exec(fs.readFileSync(path.join(root, "internal/pitch/catalog.go"), "utf8"))![1];
const asset = path.join(root, "dist-release/pitch-darwin-arm64");
fs.mkdirSync(path.dirname(asset), { recursive: true });

function run(cmd: string[], env: Record<string, string> = {}) {
	const proc = Bun.spawnSync(cmd, { cwd: root, env: { ...process.env, ...env }, stdout: "inherit", stderr: "inherit" });
	if (proc.exitCode !== 0) throw new Error(`${cmd.join(" ")} failed`);
}

run(["go", "build", "-trimpath", "-o", asset, "./cmd/pitch"], { GOOS: "darwin", GOARCH: "arm64" });
const hash = new Bun.CryptoHasher("sha256").update(fs.readFileSync(asset)).digest("hex");
fs.writeFileSync(`${asset}.sha256`, `${hash}  pitch-darwin-arm64\n`);
console.log(`pitch ${version}: ${asset} ${hash}`);

if (process.argv.includes("--publish")) {
	const install = "curl -fsSL https://raw.githubusercontent.com/iamdin/pitch/main/install.sh | sh";
	run(["gh", "release", "create", `v${version}`, asset, `${asset}.sha256`, "--title", `pitch ${version}`, "--latest", "--notes", `Install: \`${install}\``]);
}
