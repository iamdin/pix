/**
 * Build AOT variants of Pi with pitch with some patches taken out, to compare which patches are worth keeping.
 *
 *   bun bench/build-variants.ts --repo /abs/pi --out /abs/dir base= md=streaming-markdown no-hl=all-highlight-cache
 *   bun bench/build-variants.ts --repo /abs/pi --out /abs/dir --combos
 *
 * A patch is a commit `perf(<name>): ...` (and its `fix(<name>)` commits) between --tag and --branch on the fork (defaults v1.4.0 and pitch-v1.4.0).
 * A variant keeps some patches: the branch with every other patch reverted, as a commit of its own (in a scratch worktree,
 * on no branch), which `pitch prepare --repo --commit` builds and `pitch build --pi` compiles. Each argument is
 * variant=patches: a comma list of patch names, `all`, or `all-name,...` (every patch but those). Empty = no patches.
 * --combos builds every subset, named by its patches joined with `+` (`base` for none). Outputs go to <out>/<variant>
 * (never overwritten: pitch refuses an existing directory). Prints variant=/abs/pi lines for bench/run.ts --build.
 */
import fs from "node:fs";
import path from "node:path";
import util from "node:util";

const root = path.resolve(import.meta.dir, "..");
const { values: args, positionals } = util.parseArgs({
	options: {
		repo: { type: "string" },
		tag: { type: "string", default: "v1.4.0" },
		branch: { type: "string", default: "pitch-v1.4.0" },
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

// A patch is its `perf(<name>)` commit and any `fix(<name>)` commits after it (PATCHES.md), oldest first; a variant
// reverts the commits of the patches it drops, newest first.
const commits: { name: string; commit: string }[] = [];
for (const line of git("log", "--reverse", "--format=%H %s", `${args.tag}..${args.branch}`).trim().split("\n")) {
	const match = /^(\w+) (?:perf|fix)\(([\w-]+)\):/.exec(line);
	if (match) commits.push({ name: match[2], commit: match[1] });
}
const patches = [...new Set(commits.map((c) => c.name))];

function select(list: string): string[] {
	if (list === "") return [];
	if (list === "all" || list.startsWith("all-")) {
		const drop = new Set(list === "all" ? [] : list.slice(4).split(","));
		return patches.filter((name) => !drop.has(name));
	}
	for (const name of list.split(",")) if (!patches.includes(name)) throw new Error(`no patch ${name} on ${args.branch}`);
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

function run(cmd: string[], log: string) {
	const proc = Bun.spawnSync(cmd, { cwd: root });
	fs.appendFileSync(log, proc.stdout.toString() + proc.stderr.toString());
	if (proc.exitCode !== 0) throw new Error(`${cmd.slice(0, 2).join(" ")} failed, see ${log}`);
}

const pitch = path.join(root, "bin/pitch");
const lines: string[] = [];
for (const { variant, names } of variants) {
	const out = path.join(args.out, variant);
	const log = `${out}.log`;
	const tree = path.join(args.out, `${variant}.worktree`);
	git("worktree", "add", "--detach", tree, args.branch!);
	let commit: string;
	try {
		for (const { commit } of commits.filter((c) => !names.includes(c.name)).reverse()) {
			git("-C", tree, "revert", "--no-edit", commit);
		}
		commit = git("-C", tree, "rev-parse", "HEAD").trim();
	} finally {
		git("worktree", "remove", "--force", tree);
	}
	const pi = `${out}.tar.gz`;
	run([pitch, "prepare", "--repo", path.resolve(args.repo!), "--commit", commit, pi], log);
	run([pitch, "build", "--pi", pi, "--out", out], log);
	console.error(`built ${variant} (${names.join(",") || "no patches"})`);
	lines.push(`${variant}=${path.join(out, "pi")}`);
}
console.log(lines.join("\n"));
