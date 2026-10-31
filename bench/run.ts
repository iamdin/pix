/**
 * Compare Pi builds on deterministic local workloads. Rounds alternate the order of the builds, so background load hits
 * every build alike. Each run is a fresh process with its own agent directory and working directory.
 *
 * Scenarios:
 *   startup    `pi --version`: wall, CPU and peak RSS of the whole process
 *   write:KB   `pi -p`: the model streams a `write` call of KB KiB, 16 characters per chunk; the file is checked
 *   md:N       the TUI on a pseudo-terminal (160x48): CPU spent streaming an N-character Markdown answer, RSS after it
 *   tuiwrite:KB  write:KB in the TUI, which draws the call as it streams in; the file is checked
 *
 * Usage:
 *   bun bench/run.ts --build base="/abs/bun /abs/pi/packages/coding-agent/src/cli.ts" --build opt=/abs/dist/pi \
 *     --scenarios startup,write:200,md:20000 --rounds 5 --out results.jsonl
 *
 * Commands run in a temporary directory: give absolute paths. The first --build is the baseline of the summary.
 * Exit status 1 if any run fails.
 */
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import util from "node:util";
import { startFakeModel, writeContent } from "./fake-model.ts";

type Build = { name: string; argv: string[] };
type Result = { ok: boolean; error?: string; [metric: string]: number | boolean | string | undefined };

const MODEL_ARGS = ["--model", "fake/fake-model"];
const ANSI = /\x1b\[[0-9;?<>=]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[=>78MDE]|\x1b[()][A-Z0-9]/g;

function splitCommand(command: string): string[] {
	// Words separated by spaces; single or double quotes keep spaces in a word.
	return [...command.matchAll(/"([^"]*)"|'([^']*)'|(\S+)/g)].map((m) => m[1] ?? m[2] ?? m[3]);
}

function piHome(port: number): string {
	const home = fs.mkdtempSync(path.join(os.tmpdir(), "pitch-bench-home-"));
	const models = [{ id: "fake-model", contextWindow: 200000, maxTokens: 8192 }];
	const provider = { baseUrl: `http://127.0.0.1:${port}/v1`, api: "openai-completions", apiKey: "fake", models };
	fs.writeFileSync(path.join(home, "models.json"), JSON.stringify({ providers: { fake: provider } }));
	fs.writeFileSync(path.join(home, "settings.json"), JSON.stringify({ lastChangelogVersion: "9999.0.0", theme: "dark" }));
	fs.writeFileSync(path.join(home, "auth.json"), "{}");
	return home;
}

function piEnv(home: string): Record<string, string> {
	const env: Record<string, string> = {};
	for (const [k, v] of Object.entries(process.env)) {
		if (v !== undefined && !/^(BUN_|NODE_|PI_)/.test(k)) env[k] = v;
	}
	return { ...env, PI_CODING_AGENT_DIR: home, PI_OFFLINE: "1", PI_SKIP_VERSION_CHECK: "1", PI_TELEMETRY: "0", TERM: "xterm-256color" };
}

/** CPU seconds and RSS (MB) of a running process, from ps. ps reports CPU time to 0.01 s. */
function psStats(pid: number): { cpu: number; rss: number } {
	const out = Bun.spawnSync(["ps", "-o", "time=,rss=", "-p", String(pid)]).stdout.toString().trim();
	const [time = "0", rss = "0"] = out.split(/\s+/);
	const cpu = time.split(":").reduce((total, part) => total * 60 + Number(part), 0);
	return { cpu, rss: Number(rss) / 1024 };
}

async function runPlain(argv: string[], env: Record<string, string>, cwd: string, timeoutMs: number) {
	const start = performance.now();
	const proc = Bun.spawn(argv, { env, cwd, stdin: "ignore", stdout: "pipe", stderr: "pipe", timeout: timeoutMs });
	const [out, err] = await Promise.all([new Response(proc.stdout).text(), new Response(proc.stderr).text()]);
	const code = await proc.exited;
	const wall = (performance.now() - start) / 1000;
	const usage = proc.resourceUsage();
	const result: Result = {
		ok: code === 0,
		wall_s: round(wall, 4),
		cpu_s: round(Number(usage?.cpuTime.total ?? 0n) / 1e6, 4),
		peak_rss_mb: round((usage?.maxRSS ?? 0) / 1048576, 1),
	};
	return { result, output: out + err };
}

class Tty {
	text = "";
	private decoder = new TextDecoder();
	readonly proc: Bun.Subprocess;

	constructor(argv: string[], env: Record<string, string>, cwd: string) {
		this.proc = Bun.spawn(argv, {
			env,
			cwd,
			terminal: {
				cols: 160,
				rows: 48,
				data: (terminal, data) => {
					const text = this.decoder.decode(data, { stream: true });
					// Answer the background colour query, as a terminal does, so that Pi does not wait for it.
					if (text.includes("\x1b]11;?")) terminal.write("\x1b]11;rgb:0000/0000/0000\x1b\\");
					this.text += text.replace(ANSI, "");
				},
			},
		});
	}

	async waitFor(needle: string, since: number, timeoutMs: number): Promise<boolean> {
		const deadline = performance.now() + timeoutMs;
		while (!this.text.includes(needle, since)) {
			if (performance.now() > deadline || this.proc.exitCode !== null) return false;
			await Bun.sleep(10);
		}
		return true;
	}

	/** Waits until the screen has not changed for `quietMs`. */
	async settle(quietMs: number, timeoutMs: number) {
		const deadline = performance.now() + timeoutMs;
		let length = this.text.length;
		let last = performance.now();
		while (performance.now() < deadline && performance.now() - last < quietMs) {
			await Bun.sleep(10);
			if (this.text.length !== length) {
				length = this.text.length;
				last = performance.now();
			}
		}
	}

	send(data: string) {
		this.proc.terminal?.write(data);
	}

	async close() {
		// SIGTERM first, so that a build run with --cpu-prof writes its profile; SIGKILL if it does not exit.
		this.proc.kill("SIGTERM");
		const timer = setTimeout(() => this.proc.kill("SIGKILL"), 3000);
		await this.proc.exited;
		clearTimeout(timer);
		this.proc.terminal?.close();
	}
}

const round = (value: number, digits: number) => Number(value.toFixed(digits));

async function runScenario(build: Build, scenario: string, port: number, timeoutMs: number): Promise<Result> {
	const home = piHome(port);
	const cwd = fs.mkdtempSync(path.join(os.tmpdir(), "pitch-bench-cwd-"));
	const env = piEnv(home);
	const [name, arg] = scenario.split(":");
	try {
		if (name === "startup") {
			const { result, output } = await runPlain([...build.argv, "--version"], env, cwd, 60_000);
			if (!result.ok) result.error = output.slice(-300);
			return result;
		}
		if (name === "write") {
			const kb = Number(arg);
			const argv = [...build.argv, "--no-session", ...MODEL_ARGS, "-p", `SCENARIO write:${kb}`];
			const { result, output } = await runPlain(argv, env, cwd, timeoutMs);
			const file = path.join(cwd, "out.ts");
			const written = fs.existsSync(file) ? fs.readFileSync(file, "utf8") : undefined;
			if (written !== writeContent(kb)) {
				result.ok = false;
				result.error = `out.ts ${written === undefined ? "missing" : "differs"}; output: ${output.slice(-300)}`;
			}
			return result;
		}
		if (name === "md" || name === "tuiwrite") {
			// tuiwrite:KB is write:KB in the TUI, which draws the call as it streams in.
			const prompt = name === "md" ? `SCENARIO md:${arg}` : `SCENARIO write:${arg}`;
			const done = name === "md" ? "Done: md." : "Done: write.";
			const tty = new Tty([...build.argv, "--no-session", ...MODEL_ARGS], env, cwd);
			try {
				if (!(await tty.waitFor("fake-model", 0, 60_000))) {
					return { ok: false, error: `the TUI did not start: ${tty.text.slice(-300)}` };
				}
				await tty.settle(500, 5000);
				const since = tty.text.length;
				const before = psStats(tty.proc.pid);
				const start = performance.now();
				tty.send(prompt);
				await Bun.sleep(100);
				tty.send("\r");
				if (!(await tty.waitFor(done, since, timeoutMs))) {
					return { ok: false, error: `no answer: ${tty.text.slice(-300)}` };
				}
				const wall = (performance.now() - start) / 1000;
				await tty.settle(300, 5000);
				const after = psStats(tty.proc.pid);
				const result: Result = { ok: true, wall_s: round(wall, 3), cpu_s: round(after.cpu - before.cpu, 2), rss_mb: round(after.rss, 1) };
				if (name === "tuiwrite") {
					const file = path.join(cwd, "out.ts");
					if (!fs.existsSync(file) || fs.readFileSync(file, "utf8") !== writeContent(Number(arg))) {
						return { ok: false, error: "out.ts missing or different" };
					}
				}
				return result;
			} finally {
				await tty.close();
			}
		}
		throw new Error(`unknown scenario: ${scenario}`);
	} finally {
		fs.rmSync(home, { recursive: true, force: true });
		fs.rmSync(cwd, { recursive: true, force: true });
	}
}

function median(values: number[]): number {
	const sorted = [...values].sort((a, b) => a - b);
	const mid = sorted.length >> 1;
	return sorted.length % 2 ? sorted[mid] : (sorted[mid - 1] + sorted[mid]) / 2;
}

/** Percent change of each build's median against the baseline's, by "scenario.metric". */
function changes(rows: (Result & { build: string; scenario: string })[], builds: Build[], scenarios: string[]) {
	const result = new Map<string, Map<string, number>>();
	for (const scenario of scenarios) {
		for (const metric of ["wall_s", "cpu_s", "peak_rss_mb", "rss_mb"]) {
			const medianOf = (build: string) => {
				const values = rows
					.filter((r) => r.build === build && r.scenario === scenario && r.ok && typeof r[metric] === "number")
					.map((r) => r[metric] as number);
				return values.length ? median(values) : undefined;
			};
			const base = medianOf(builds[0].name);
			if (!base) continue;
			for (const build of builds.slice(1)) {
				const med = medianOf(build.name);
				if (med === undefined) continue;
				if (!result.has(build.name)) result.set(build.name, new Map());
				result.get(build.name)!.set(`${scenario}.${metric}`, (med / base - 1) * 100);
			}
		}
	}
	return result;
}

function summarize(rows: (Result & { build: string; scenario: string })[], builds: Build[], scenarios: string[]) {
	const base = builds[0].name;
	console.log(`\n${"scenario".padEnd(12)} ${"build".padEnd(12)} ${"metric".padEnd(12)} ${"median".padStart(10)} ${"min–max".padStart(22)} ${`vs ${base}`.padStart(10)}  n`);
	for (const scenario of scenarios) {
		for (const metric of ["wall_s", "cpu_s", "peak_rss_mb", "rss_mb"]) {
			let baseMedian: number | undefined;
			for (const build of builds) {
				const values = rows
					.filter((r) => r.build === build.name && r.scenario === scenario && r.ok && typeof r[metric] === "number")
					.map((r) => r[metric] as number);
				if (values.length === 0) continue;
				const med = median(values);
				if (build.name === base) baseMedian = med;
				const change = baseMedian && build.name !== base ? `${((med / baseMedian - 1) * 100).toFixed(1)}%` : "";
				const range = `${Math.min(...values).toFixed(3)}–${Math.max(...values).toFixed(3)}`;
				console.log(
					`${scenario.padEnd(12)} ${build.name.padEnd(12)} ${metric.padEnd(12)} ${med.toFixed(3).padStart(10)} ${range.padStart(22)} ${change.padStart(10)}  ${values.length}`,
				);
			}
		}
	}
}

const { values: args } = util.parseArgs({
	options: {
		build: { type: "string", multiple: true },
		scenarios: { type: "string", default: "startup,write:200,md:20000" },
		rounds: { type: "string", default: "5" },
		warmup: { type: "string", default: "1" },
		"pace-ms": { type: "string", default: "20" },
		timeout: { type: "string", default: "600" },
		out: { type: "string" },
		expect: { type: "string", multiple: true },
		"max-regress": { type: "string" },
	},
});

const builds: Build[] = (args.build ?? []).map((spec) => {
	const at = spec.indexOf("=");
	if (at < 1) throw new Error(`--build ${spec}: expected name=command`);
	return { name: spec.slice(0, at), argv: splitCommand(spec.slice(at + 1)) };
});
if (builds.length === 0) throw new Error("give at least one --build name=command");
const scenarios = args.scenarios.split(",");
const rounds = Number(args.rounds);
const warmup = Number(args.warmup);
const timeoutMs = Number(args.timeout) * 1000;

const server = startFakeModel(Number(args["pace-ms"]));
const rows: (Result & { build: string; scenario: string; round: number })[] = [];
let failed = false;
for (let round = -warmup; round < rounds; round++) {
	const order = round % 2 === 0 ? builds : [...builds].reverse();
	for (const scenario of scenarios) {
		for (const build of order) {
			const row = { build: build.name, scenario, round, ...(await runScenario(build, scenario, server.port, timeoutMs)) };
			console.log(JSON.stringify(row));
			if (round < 0) continue;
			rows.push(row);
			failed ||= !row.ok;
			if (args.out) fs.appendFileSync(args.out, `${JSON.stringify(row)}\n`);
		}
	}
}
server.stop(true);
summarize(rows, builds, scenarios);
if (failed) process.exit(1);

// Gates, on the medians of every build after the first against the first:
//   --expect md:20000.cpu_s<=-30   that change must be at most -30% (repeatable)
//   --max-regress 5                no other scenario.metric may be more than 5% worse
let gateFailed = false;
const expected = new Map<string, number>();
for (const spec of args.expect ?? []) {
	const m = /^(.+)<=(-?\d+(?:\.\d+)?)$/.exec(spec);
	if (!m) throw new Error(`--expect ${spec}: expected scenario.metric<=PERCENT`);
	expected.set(m[1], Number(m[2]));
}
for (const [build, byKey] of changes(rows, builds, scenarios)) {
	for (const [key, limit] of expected) {
		const change = byKey.get(key);
		if (change === undefined || change > limit) {
			console.log(`GATE FAIL ${build} ${key}: ${change?.toFixed(1) ?? "no data"}% > ${limit}%`);
			gateFailed = true;
		}
	}
	if (args["max-regress"] !== undefined) {
		for (const [key, change] of byKey) {
			if (!expected.has(key) && change > Number(args["max-regress"])) {
				console.log(`GATE FAIL ${build} ${key}: ${change.toFixed(1)}% worse (limit ${args["max-regress"]}%)`);
				gateFailed = true;
			}
		}
	}
}
if (args.expect || args["max-regress"]) console.log(gateFailed ? "GATE: FAIL" : "GATE: PASS");
process.exit(gateFailed ? 2 : 0);
