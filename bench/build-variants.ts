/**
 * Build AOT variants of Pi with pitch from the patch diffs, to compare which patches are worth keeping.
 *
 *   bun bench/build-variants.ts --tag v1.4.0 --out /abs/dir base= md=streaming-markdown no-hl=all-highlight-cache
 *   bun bench/build-variants.ts --tag v1.4.0 --out /abs/dir --combos
 *
 * A patch is a directory `docs/pi-perf/<name>/` with a `<tag>.diff`; the patches apply in any order (name order here).
 * Each argument is variant=patches: a comma list of patch names, `all`, or `all-name,...` (every patch but those). Empty
 * = no patches. --combos builds every subset, named by its patches joined with `+` (`base` for none). Outputs go to
 * <out>/<variant> (never overwritten: pitch refuses an existing directory). Prints variant=/abs/pi lines for
 * bench/run.ts --build.
 */
import fs from "node:fs";
import path from "node:path";
import util from "node:util";

const root = path.resolve(import.meta.dir, "..");
const { values: args, positionals } = util.parseArgs({
	options: { tag: { type: "string", default: "v1.4.0" }, out: { type: "string" }, combos: { type: "boolean" } },
	allowPositionals: true,
});
if (!args.out) throw new Error("--out /abs/dir is required");
fs.mkdirSync(args.out, { recursive: true });

const specDir = path.join(root, "docs/pi-perf");
const patches = fs
	.readdirSync(specDir)
	.filter((name) => fs.existsSync(path.join(specDir, name, `${args.tag}.diff`)))
	.sort();

function select(list: string): string[] {
	if (list === "") return [];
	if (list === "all" || list.startsWith("all-")) {
		const drop = new Set(list === "all" ? [] : list.slice(4).split(","));
		return patches.filter((name) => !drop.has(name));
	}
	return list.split(",");
}

const variants = args.combos
	? Array.from({ length: 2 ** patches.length }, (_, mask) => {
			const names = patches.filter((_, i) => mask & (1 << i));
			return { variant: names.join("+") || "base", names };
		})
	: positionals.map((arg) => {
			const at = arg.indexOf("=");
			if (at < 1) throw new Error(`${arg}: expected variant=patches`);
			return { variant: arg.slice(0, at), names: select(arg.slice(at + 1)) };
		});

const lines: string[] = [];
for (const { variant, names } of variants) {
	const flags = names.flatMap((name) => {
		const diff = path.join(specDir, name, `${args.tag}.diff`);
		if (!fs.existsSync(diff)) throw new Error(`no ${args.tag}.diff for patch ${name}`);
		return ["--patch", diff];
	});
	const out = path.join(args.out, variant);
	const log = `${out}.log`;
	const proc = Bun.spawnSync([path.join(root, "bin/pitch"), "build", "--features", "none", ...flags, "--out", out], { cwd: root });
	fs.writeFileSync(log, proc.stdout.toString() + proc.stderr.toString());
	if (proc.exitCode !== 0) throw new Error(`${variant}: pitch build failed, see ${log}`);
	console.error(`built ${variant} (${names.join(",") || "no patches"})`);
	lines.push(`${variant}=${path.join(out, "pi")}`);
}
console.log(lines.join("\n"));
