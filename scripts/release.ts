/**
 * Build pitch for each platform into dist-release/pitch-<goos>-<goarch>, the assets install.sh downloads, and check
 * each one where it runs: `pitch build` and bench/e2e on this machine for its own platform, over ssh for the others, or
 * on GitHub Actions (.github/workflows/check-release.yml) with --host PLATFORM=gh. --publish then creates the GitHub
 * release v<Version> as a draft, runs the GitHub checks on it, publishes it and installs it back with install.sh.
 *
 *   bun scripts/release.ts --host linux/amd64=root@100.88.65.47 --host linux/arm64=gh [--unchecked PLATFORM] [--publish]
 *
 * Built locally: the embedded Bun canary archives (internal/pitch/runtime-data/<goos>-<goarch>/bun.zip) are not in git,
 * and canary downloads expire. The patched, built and tested Pi for each platform (runtime-data/<goos>-<goarch>/pi.tar.gz)
 * is prepared here too (needs git, Node 24+ and npm 11+), so `pitch build` on users' machines only compiles.
 *
 * Every platform must be checked (--host) unless --unchecked names it; GitHub checks need --publish. --publish also needs a clean main that is
 * pushed and a version not released yet. The pinned Pi commit must be the head of its fork branch.
 */
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import util from "node:util";

const root = path.resolve(import.meta.dir, "..");
const { values: args } = util.parseArgs({
	options: {
		publish: { type: "boolean" },
		host: { type: "string", multiple: true, default: [] },
		unchecked: { type: "string", multiple: true, default: [] },
	},
});
const version = /const Version = "(.+)"/.exec(fs.readFileSync(path.join(root, "internal/pitch/catalog.go"), "utf8"))![1];
const tag = `v${version}`;
const pins = JSON.parse(fs.readFileSync(path.join(root, "internal/pitch/data/sources.lock.json"), "utf8"));
const platforms = Object.keys(pins.bun.platforms);
const local = `${process.platform}/${process.arch === "x64" ? "amd64" : process.arch}`;
const hosts = new Map(args.host!.map((h) => h.split("=") as [string, string]));
const dist = path.join(root, "dist-release");
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "pitch-release-"));
const dataDir = (platform: string) => path.join(root, "internal/pitch/runtime-data", platform.replace("/", "-"));
const assetName = (platform: string) => `pitch-${platform.replace("/", "-")}`;

function run(cmd: string[], env: Record<string, string> = {}) {
	console.error(`$ ${cmd.join(" ")}`);
	const proc = Bun.spawnSync(cmd, { cwd: root, env: { ...process.env, ...env }, stdout: "inherit", stderr: "inherit" });
	if (proc.exitCode !== 0) throw new Error(`${cmd[0]} failed`);
}
function output(cmd: string[]): string {
	const proc = Bun.spawnSync(cmd, { cwd: root, stderr: "pipe" });
	if (proc.exitCode !== 0) throw new Error(`${cmd.join(" ")}: ${proc.stderr}`);
	return proc.stdout.toString().trim();
}
function fail(message: string): never {
	throw new Error(`release: ${message}`);
}
const sha256 = (file: string) => new Bun.CryptoHasher("sha256").update(fs.readFileSync(file)).digest("hex");

// Guards, before anything is built.
for (const platform of platforms) {
	if (platform !== local && !hosts.has(platform) && !args.unchecked!.includes(platform))
		fail(`nothing checks ${platform}: give --host ${platform}=SSH-TARGET or --unchecked ${platform}`);
	const zip = path.join(dataDir(platform), "bun.zip");
	if (!fs.existsSync(zip) || sha256(zip) !== pins.bun.platforms[platform].sha256) fail(`${zip} is missing or not the pinned Bun`);
}
const onGitHub = platforms.filter((p) => hosts.get(p) === "gh");
if (onGitHub.length > 0 && !args.publish) fail(`${onGitHub.join(", ")}: GitHub checks run on the draft release, with --publish`);
if (args.publish) {
	if (output(["git", "status", "--porcelain"])) fail("the work tree is not clean");
	if (output(["git", "branch", "--show-current"]) !== "main") fail("not on main");
	run(["git", "fetch", "-q", "origin", "main"]);
	if (output(["git", "rev-parse", "HEAD"]) !== output(["git", "rev-parse", "origin/main"])) fail("main is not pushed");
	if (Bun.spawnSync(["gh", "release", "view", tag], { cwd: root }).exitCode === 0) fail(`${tag} is already released`);
}
const head = output(["git", "ls-remote", pins.pi.repository, `refs/heads/${pins.pi.branch}`]).split(/\s/)[0];
if (head !== pins.pi.commit) fail(`${pins.pi.repository} ${pins.pi.branch} is at ${head}, not the pinned ${pins.pi.commit}`);
run(["go", "vet", "./..."]);
run(["go", "test", "-count=1", "./..."]);

// Prepare Pi for every platform, then build pitch with it.
const builder = path.join(scratch, "pitch");
run(["go", "build", "-o", builder, "./cmd/pitch"]);
for (const platform of platforms) run([builder, "prepare", "--platform", platform, path.join(dataDir(platform), "pi.tar.gz")]);
fs.mkdirSync(dist, { recursive: true });
const assets: string[] = [];
for (const platform of platforms) {
	const [GOOS, GOARCH] = platform.split("/");
	const asset = path.join(dist, assetName(platform));
	run(["go", "build", "-trimpath", "-o", asset, "./cmd/pitch"], { GOOS, GOARCH });
	fs.writeFileSync(`${asset}.sha256`, `${sha256(asset)}  ${assetName(platform)}\n`);
	assets.push(asset, `${asset}.sha256`);
}

// Check each asset where it runs: it builds Pi, and that Pi passes bench/e2e.
const e2e = path.join(scratch, "e2e.tar.gz");
run(["tar", "-czf", e2e, "-C", path.join(root, "bench"), "e2e"]);
for (const platform of platforms) {
	const asset = path.join(dist, assetName(platform));
	if (platform === local) {
		run([asset, "build", "--out", path.join(scratch, "build")]);
		run(["bun", path.join(root, "bench/e2e/run.ts"), path.join(scratch, "build/pi")]);
	} else if (hosts.get(platform) === "gh") {
		console.error(`release: ${platform} is checked on GitHub, on the draft`);
	} else if (hosts.has(platform)) {
		const target = hosts.get(platform)!;
		const dir = output(["ssh", target, "mktemp -d"]);
		run(["scp", "-q", asset, e2e, `${target}:${dir}/`]);
		// bench/e2e runs with the Bun that `pitch build` unpacked into its cache.
		const script = `set -e; cd ${dir}; chmod +x ${assetName(platform)}; tar -xzf e2e.tar.gz; ./${assetName(platform)} build --out build; bun=$(ls -t ~/.cache/pitch/runtime/*/bun | head -1); $bun e2e/run.ts ${dir}/build/pi; rm -rf ${dir}`;
		run(["ssh", target, script]);
	} else {
		console.error(`release: ${platform} is not checked (--unchecked)`);
	}
}
for (const platform of platforms) console.log(`pitch ${version} ${platform}: ${sha256(path.join(dist, assetName(platform)))}`);

if (args.publish) {
	// A draft first, so that nobody downloads assets that differ from the checked ones.
	const install = "curl -fsSL https://raw.githubusercontent.com/iamdin/pitch/main/install.sh | sh";
	run(["gh", "release", "create", tag, ...assets, "--draft", "--title", `pitch ${version}`, "--notes", `Install: \`${install}\``]);
	const download = path.join(scratch, "download");
	run(["gh", "release", "download", tag, "--dir", download]);
	for (const asset of assets) {
		if (sha256(path.join(download, path.basename(asset))) !== sha256(asset)) fail(`the uploaded ${path.basename(asset)} differs; ${tag} stays a draft`);
	}
	if (onGitHub.length > 0) {
		const since = new Date(Date.now() - 60_000).toISOString(); // allow for clock skew
		run(["gh", "workflow", "run", "check-release.yml", "-f", `tag=${tag}`, "-f", `platforms=${JSON.stringify(onGitHub)}`]);
		let id = "";
		for (let i = 0; i < 30 && !id; i++) {
			Bun.sleepSync(2000);
			id = output(["gh", "run", "list", "--workflow", "check-release.yml", "--event", "workflow_dispatch", "-L", "1", "--json", "databaseId,createdAt", "--jq", `.[] | select(.createdAt >= "${since.slice(0, 19)}") | .databaseId`]);
		}
		if (!id) fail("the check-release workflow did not start");
		if (Bun.spawnSync(["gh", "run", "watch", id, "--exit-status"], { cwd: root, stdout: "inherit", stderr: "inherit" }).exitCode !== 0)
			fail(`the GitHub checks failed (gh run view ${id} --log-failed); ${tag} stays a draft`);
	}
	run(["gh", "release", "edit", tag, "--draft=false", "--latest"]);
	const bin = path.join(scratch, "installed");
	run(["sh", path.join(root, "install.sh")], { PITCH_INSTALL_DIR: bin });
	if (output([path.join(bin, "pitch"), "--version"]) !== version) fail("install.sh did not install this version");
	console.log(`released ${tag}`);
}
fs.rmSync(scratch, { recursive: true, force: true });
