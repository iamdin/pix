/**
 * Build AOT variants of Pi with pitch with some patches taken out, to compare which patches are worth keeping.
 *
 *   bun bench/build-variants.ts --repo /abs/pi --out /abs/dir base= md=streaming-markdown no-hl=all-highlight-cache
 *   bun bench/build-variants.ts --repo /abs/pi --out /abs/dir --combos
 *
 * A patch is a commit `perf(<name>): ...` between --tag and --branch on the fork (defaults v1.4.0 and patch-v1.4.0;
 * the branch must be at the commit pinned in sources.lock.json). A variant keeps some patches: pitch builds the branch with the reverse diff of every
 * other patch passed as --patch. Each argument is variant=patches: a comma list of patch names, `all`, or
 * `all-name,...` (every patch but those). Empty = no patches. --combos builds every subset, named by its patches joined
 * with `+` (`base` for none). Outputs go to <out>/<variant> (never overwritten: pitch refuses an existing directory).
 * Prints variant=/abs/pi lines for bench/run.ts --build.
 */
import fs from "node:fs";
import path from "node:path";
import util from "node:util";

const root = path.resolve(import.meta.dir, "..");
const { values: args, positionals } = util.parseArgs({
	options: {
		repo: { type: "string" },
		tag: { type: "string", default: "v1.4.0" },
		branch: { type: "string", default: "patch-v1.4.0" },
		out: { type: "string" },
		combos: { type: "boolean" },
	},
	allowPositionals: true,
});
if (!args.out || !args.repo) throw new Error("--repo /abs/pi and --out /abs/dir are required");
fs.mkdirSync(args.out, { recursive: true });

function git(...cmd: string[]): string {
	const proc = Bun.spawnSync(["git", "-C", args.repo!, ...cmd]);
	if (proc.exitCode !== 0) throw new Error(`git ${cmd.join(" ")}: ${proc.stderr}`);
	return proc.stdout.toString();
}

// name -> commit, oldest first; the reverse diffs are applied newest first.
const commits = new Map<string, string>();
for (const line of git("log", "--reverse", "--format=%H %s", `${args.tag}..${args.branch}`).trim().split("\n")) {
	const match = /^(\w+) perf\(([\w-]+)\):/.exec(line);
	if (match) commits.set(match[2], match[1]);
}
const patches = [...commits.keys()];

function select(list: string): string[] {
	if (list === "") return [];
	if (list === "all" || list.startsWith("all-")) {
		const drop = new Set(list === "all" ? [] : list.slice(4).split(","));
		return patches.filter((name) => !drop.has(name));
	}
	for (const name of list.split(",")) if (!commits.has(name)) throw new Error(`no patch ${name} on ${args.branch}`);
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
	const out = path.join(args.out, variant);
	const flags = patches
		.filter((name) => !names.includes(name))
		.reverse()
		.flatMap((name) => {
			const revert = path.join(args.out!, `revert-${name}.patch`);
			const commit = commits.get(name)!;
			fs.writeFileSync(revert, git("diff", commit, `${commit}^`));
			return ["--patch", revert];
		});
	const log = `${out}.log`;
	const proc = Bun.spawnSync([path.join(root, "bin/pitch"), "build", ...flags, "--out", out], { cwd: root });
	fs.writeFileSync(log, proc.stdout.toString() + proc.stderr.toString());
	if (proc.exitCode !== 0) throw new Error(`${variant}: pitch build failed, see ${log}`);
	console.error(`built ${variant} (${names.join(",") || "no patches"})`);
	lines.push(`${variant}=${path.join(out, "pi")}`);
}
console.log(lines.join("\n"));
