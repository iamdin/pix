/**
 * Releases pix, like `npm version` and `npm publish` in one:
 *
 *   bun run release patch               # 0.0.4 -> 0.0.5 (also minor, major, or a version such as 0.1.0)
 *
 * It sets the version (internal/pix/catalog.go and the README), runs the Go tests, commits "Version X", tags vX and
 * pushes the tag: GitHub Actions builds, checks and publishes the release (.github/workflows/release.yml). It watches
 * that run, and once it succeeded pushes main. main goes last because pix before 0.0.4 updates with main's
 * install.sh, which must not run ahead of the release. If the run fails, nothing is published from it and main stays
 * local: fix, commit, and release the next version.
 */
import fs from "node:fs";
import path from "node:path";

const root = path.resolve(import.meta.dir, "..");
const catalog = path.join(root, "internal/pix/catalog.go");
const readme = path.join(root, "README.md");

function run(cmd: string[]) {
	console.error(`$ ${cmd.join(" ")}`);
	if (Bun.spawnSync(cmd, { cwd: root, stdout: "inherit", stderr: "inherit" }).exitCode !== 0) throw new Error(`${cmd.join(" ")} failed`);
}
function output(cmd: string[]): string {
	const proc = Bun.spawnSync(cmd, { cwd: root, stderr: "pipe" });
	if (proc.exitCode !== 0) throw new Error(`${cmd.join(" ")}: ${proc.stderr}`);
	return proc.stdout.toString().trim();
}

Bun.spawnSync(["git", "fetch", "-q", "--tags", "origin"], { cwd: root });
const bump = process.argv[2];
const current = /const Version = "(\d+)\.(\d+)\.(\d+)"/.exec(fs.readFileSync(catalog, "utf8"));
if (!current) throw new Error("no Version in catalog.go");
// From the newest of the version in catalog.go and the released tags (a tag may have been pushed by hand).
const newer = (a: number[], b: number[]) => a[0] - b[0] || a[1] - b[1] || a[2] - b[2];
const versions = [current.slice(1).map(Number)];
for (const t of output(["git", "tag", "-l", "v*"]).split("\n")) {
	const m = /^v(\d+)\.(\d+)\.(\d+)$/.exec(t);
	if (m) versions.push(m.slice(1).map(Number));
}
const [major, minor, patch] = versions.sort(newer).at(-1)!;
const version =
	bump === "patch" ? `${major}.${minor}.${patch + 1}`
	: bump === "minor" ? `${major}.${minor + 1}.0`
	: bump === "major" ? `${major + 1}.0.0`
	: /^\d+\.\d+\.\d+$/.test(bump ?? "") ? bump!
	: (() => { throw new Error("usage: bun run release patch|minor|major|X.Y.Z"); })();
const tag = `v${version}`;

if (output(["git", "branch", "--show-current"]) !== "main") throw new Error("not on main");
if (output(["git", "status", "--porcelain", "--untracked-files=no"])) throw new Error("commit or stash the changes first");
run(["git", "fetch", "-q", "--tags", "origin", "main"]);
if (output(["git", "rev-list", "--count", "HEAD..origin/main"]) !== "0") throw new Error("origin/main has commits that main has not: pull first");
if (output(["git", "tag", "-l", tag])) throw new Error(`${tag} exists already`);

fs.writeFileSync(catalog, fs.readFileSync(catalog, "utf8").replace(current[0], `const Version = "${version}"`));
fs.writeFileSync(readme, fs.readFileSync(readme, "utf8").replace(/\*\*Currently [^,]+, for/, `**Currently ${version}, for`));
run(["go", "vet", "./..."]);
run(["go", "test", "-count=1", "./..."]);
run(["git", "commit", "-q", "-m", `Version ${version}`, catalog, readme]);
run(["git", "tag", tag]);
run(["git", "push", "-q", "origin", tag]);

// The run the tag started.
let id = "";
for (let i = 0; i < 30 && !id; i++) {
	Bun.sleepSync(2000);
	id = output(["gh", "run", "list", "--workflow", "release.yml", "--branch", tag, "-L", "1", "--json", "databaseId", "-q", ".[0].databaseId"]);
}
if (!id) throw new Error(`the release workflow did not start for ${tag}; see https://github.com/iamdin/pix/actions`);
console.error(`Releasing ${tag}: https://github.com/iamdin/pix/actions/runs/${id}`);
// Polled rather than `gh run watch`, which gives up on the first network error.
let state = { status: "", conclusion: "", jobs: [] as { name: string; conclusion: string }[] };
const done = new Set<string>();
while (state.status !== "completed") {
	Bun.sleepSync(30_000);
	const proc = Bun.spawnSync(["gh", "run", "view", id, "--json", "status,conclusion,jobs"], { cwd: root, stderr: "pipe" });
	if (proc.exitCode !== 0) continue; // a network error: ask again
	state = JSON.parse(proc.stdout.toString());
	for (const job of state.jobs.filter((j) => j.conclusion && !done.has(j.name))) {
		done.add(job.name);
		console.error(`${job.conclusion === "success" ? "✓" : "✗"} ${job.name}`);
	}
}
if (state.conclusion !== "success") {
	throw new Error(`the release of ${tag} failed (gh run view ${id} --log-failed); main is not pushed`);
}
run(["git", "push", "-q", "origin", "main"]);
console.log(`Released ${tag}: https://github.com/iamdin/pix/releases/tag/${tag}`);
