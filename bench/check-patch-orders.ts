/**
 * Check that the patches apply on the tag in every order and every subset, and that every order of all of them gives the
 * same tree. Uses a temporary index in the Pi repository; its work tree is not touched.
 *
 *   bun bench/check-patch-orders.ts --repo /abs/pi --tag v1.4.0
 */
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import util from "node:util";

const { values: args } = util.parseArgs({
	options: { repo: { type: "string" }, tag: { type: "string", default: "v1.4.0" } },
});
if (!args.repo) throw new Error("--repo /abs/pi is required");
const specDir = path.resolve(import.meta.dir, "../docs/pi-perf");
const diffs = fs
	.readdirSync(specDir)
	.map((name) => path.join(specDir, name, `${args.tag}.diff`))
	.filter((diff) => fs.existsSync(diff))
	.sort();
const env = { ...process.env, GIT_INDEX_FILE: path.join(fs.mkdtempSync(path.join(os.tmpdir(), "pi-perf-")), "index") };
const git = (...a: string[]) => Bun.spawnSync(["git", ...a], { cwd: args.repo, env });
const name = (diff: string) => path.basename(path.dirname(diff));

let orders = 0;
const fails: string[] = [];
const fullTrees = new Set<string>();
function walk(applied: string[], rest: string[]) {
	if (applied.length) {
		orders++;
		git("read-tree", args.tag!);
		const bad = applied.find((d) => git("apply", "--cached", d).exitCode !== 0);
		if (bad) fails.push(`${applied.map(name).join(" → ")}: ${name(bad)} does not apply`);
		else if (!rest.length) fullTrees.add(git("write-tree").stdout.toString().trim());
	}
	rest.forEach((d, i) => walk([...applied, d], rest.filter((_, j) => j !== i)));
}
walk([], diffs);
console.log(`${diffs.length} patches, ${orders} ordered subsets, ${fails.length} fail; full set trees: ${[...fullTrees].join(", ")}`);
for (const f of fails) console.log(f);
if (fails.length || fullTrees.size !== 1) process.exit(1);
