/**
 * Build pitch for each platform into dist-release/pitch-<goos>-<goarch>, the assets install.sh downloads; --publish
 * also creates the GitHub release v<Version>.
 *
 *   bun scripts/release.ts [--publish]
 *
 * Built locally: the embedded Bun canary archives (internal/pitch/runtime-data/<goos>-<goarch>/bun.zip) are not in git,
 * and canary downloads expire. The patched, built and tested Pi for each platform (runtime-data/<goos>-<goarch>/pi.tar.gz)
 * is prepared here too (needs git, Node 24+ and npm 11+), so `pitch build` on users' machines only compiles.
 */
import fs from "node:fs";
import path from "node:path";

const root = path.resolve(import.meta.dir, "..");
const version = /const Version = "(.+)"/.exec(fs.readFileSync(path.join(root, "internal/pitch/catalog.go"), "utf8"))![1];
const platforms = ["darwin/arm64", "linux/amd64", "linux/arm64"];
const dist = path.join(root, "dist-release");
fs.mkdirSync(dist, { recursive: true });

function run(cmd: string[], env: Record<string, string> = {}) {
	const proc = Bun.spawnSync(cmd, { cwd: root, env: { ...process.env, ...env }, stdout: "inherit", stderr: "inherit" });
	if (proc.exitCode !== 0) throw new Error(`${cmd.join(" ")} failed`);
}

const host = path.join(dist, "pitch-host");
run(["go", "build", "-o", host, "./cmd/pitch"]);
for (const platform of platforms) {
	const data = path.join(root, "internal/pitch/runtime-data", platform.replace("/", "-"));
	run([host, "prepare", "--platform", platform, path.join(data, "pi.tar.gz")]);
}
fs.rmSync(host);
const assets: string[] = [];
for (const platform of platforms) {
	const [GOOS, GOARCH] = platform.split("/");
	const name = `pitch-${GOOS}-${GOARCH}`;
	const asset = path.join(dist, name);
	run(["go", "build", "-trimpath", "-o", asset, "./cmd/pitch"], { GOOS, GOARCH });
	const hash = new Bun.CryptoHasher("sha256").update(fs.readFileSync(asset)).digest("hex");
	fs.writeFileSync(`${asset}.sha256`, `${hash}  ${name}\n`);
	console.log(`pitch ${version} ${platform}: ${asset} ${hash}`);
	assets.push(asset, `${asset}.sha256`);
}

if (process.argv.includes("--publish")) {
	const install = "curl -fsSL https://raw.githubusercontent.com/iamdin/pitch/main/install.sh | sh";
	run(["gh", "release", "create", `v${version}`, ...assets, "--title", `pitch ${version}`, "--latest", "--notes", `Install: \`${install}\``]);
}
