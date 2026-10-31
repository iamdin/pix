/**
 * Shared pieces of the end-to-end tests: a scripted OpenAI-compatible fake model, a fresh agent directory per test, and
 * drivers for print, RPC and interactive (PTY) Pi.
 */
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

export const pi = path.resolve(process.argv[2] ?? "");
export const fixtures = path.join(import.meta.dir, "fixtures");

// ---- Fake model -------------------------------------------------------------------------------------------------

export type Call = { name: string; args: Record<string, unknown> };
/** One answer of the model: text, tool calls (run in parallel), a slow text, or an HTTP error. */
export type Reply = { text: string; usage?: number } | { calls: Call[] } | { slow: true } | { status: number };
export type Request = { body: any; headers: Record<string, string> };

export const requests: Request[] = [];
let steps: Reply[] = [];

/** The model answers with these replies in order, then "Done.". A user message containing SLOW gets a slow answer. */
export function script(...replies: (Reply | Call)[]) {
	steps = replies.map((r) => ("name" in r ? { calls: [r] } : r));
	requests.length = 0;
}

const chunk = (delta: object, finish_reason: string | null = null, usage?: number) => ({
	id: "x",
	object: "chat.completion.chunk",
	created: 0,
	model: "fake",
	choices: [{ index: 0, delta, finish_reason }],
	...(usage ? { usage: { prompt_tokens: usage, completion_tokens: 5, total_tokens: usage + 5 } } : {}),
});
const sse = (chunks: object[]) =>
	new Response(`${chunks.map((c) => `data: ${JSON.stringify(c)}\n\n`).join("")}data: [DONE]\n\n`, {
		headers: { "content-type": "text/event-stream" },
	});

function slowAnswer() {
	let timer: Timer | undefined;
	const encoder = new TextEncoder();
	const stream = new ReadableStream({
		start(controller) {
			let i = 0;
			const next = () => {
				if (i === 40) {
					controller.enqueue(encoder.encode(`data: ${JSON.stringify(chunk({}, "stop"))}\n\ndata: [DONE]\n\n`));
					controller.close();
					return;
				}
				controller.enqueue(encoder.encode(`data: ${JSON.stringify(chunk({ role: "assistant", content: `word${i++} ` }))}\n\n`));
				timer = setTimeout(next, 50);
			};
			next();
		},
		cancel: () => clearTimeout(timer),
	});
	return new Response(stream, { headers: { "content-type": "text/event-stream" } });
}

function answer(reply: Reply, index: number): Response {
	if ("status" in reply) return new Response(JSON.stringify({ error: { message: "fake failure" } }), { status: reply.status });
	if ("slow" in reply) return slowAnswer();
	if ("text" in reply) return sse([chunk({ role: "assistant", content: reply.text }), chunk({}, "stop", reply.usage)]);
	const tool_calls = reply.calls.map((c, i) => ({
		index: i,
		id: `call_${index}_${i}`,
		type: "function",
		function: { name: c.name, arguments: JSON.stringify(c.args) },
	}));
	return sse([chunk({ role: "assistant", tool_calls }), chunk({}, "tool_calls")]);
}

export const server = Bun.serve({
	port: 0,
	idleTimeout: 120,
	async fetch(req) {
		const body = await req.json();
		requests.push({ body, headers: Object.fromEntries(req.headers) });
		const lastUser = JSON.stringify(body.messages?.findLast((m: any) => m.role === "user")?.content ?? "");
		// Summaries for compaction are plain text requests; answer them as such.
		if (lastUser.includes("SLOW")) return slowAnswer();
		const reply = steps[requests.length - 1] ?? { text: "Done." };
		return answer(reply, requests.length);
	},
});

/** The tool results and user messages of the last request, as JSON. */
export const lastTurn = () =>
	JSON.stringify(requests.at(-1)?.body.messages?.filter((m: any) => m.role === "tool" || m.role === "user") ?? []);
export const allRequests = () => JSON.stringify(requests.map((r) => r.body));
export const toolNames = (i: number) => (requests[i]?.body.tools ?? []).map((t: any) => t.function.name) as string[];
export const systemPrompt = (i = 0) => JSON.stringify(requests[i]?.body.messages?.filter((m: any) => m.role === "system" || m.role === "developer") ?? []);

// ---- Agent directory ---------------------------------------------------------------------------------------------

export type Agent = { home: string; cwd: string; env: Record<string, string> };

export function agent(options: { settings?: object; mcp?: object } = {}): Agent {
	const home = fs.mkdtempSync(path.join(os.tmpdir(), "pi-e2e-"));
	const cwd = path.join(home, "work");
	fs.mkdirSync(cwd);
	fs.writeFileSync(path.join(cwd, "hello.txt"), "hello pitch\nsecond line\nthird line\n");
	const base = { contextWindow: 100000, maxTokens: 1000, input: ["text", "image"] };
	const models = [
		{ id: "fake", name: "fake", ...base },
		{ id: "fake2", name: "fake2", ...base },
		{ id: "small", name: "small", ...base, contextWindow: 4000 },
		{ id: "think", name: "think", ...base, reasoning: true },
	];
	fs.writeFileSync(
		path.join(home, "models.json"),
		JSON.stringify({ providers: { fake: { baseUrl: `http://127.0.0.1:${server.port}/v1`, api: "openai-completions", apiKey: "fake-key", models } } }),
	);
	if (options.settings) fs.writeFileSync(path.join(home, "settings.json"), JSON.stringify(options.settings));
	if (options.mcp) fs.writeFileSync(path.join(home, "mcp.json"), JSON.stringify({ mcpServers: options.mcp }));
	const env = {
		...process.env,
		HOME: home,
		PI_CODING_AGENT_DIR: home,
		PI_SKIP_VERSION_CHECK: "1",
		PI_TELEMETRY: "0",
		TERM: "xterm-256color",
	} as Record<string, string>;
	return { home, cwd, env };
}

/** A 3000×3000 PNG (larger than the read tool sends, so it is resized) or a 1×1 one. */
export function png(file: string, size = 3000) {
	fs.writeFileSync(file, Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==", "base64"));
	if (size > 1) Bun.spawnSync(["sips", "-z", String(size), String(size), file]);
}

export const mcpEcho = (extra: object = {}) => ({ t: { command: "node", args: [path.join(fixtures, "mcp-echo-server.mjs")], ...extra } });

// ---- Print mode -------------------------------------------------------------------------------------------------

export async function run(a: Agent, args: string[], options: { stdin?: string; timeoutMs?: number } = {}) {
	const proc = Bun.spawn([pi, ...args], {
		cwd: a.cwd,
		env: a.env,
		stdin: options.stdin === undefined ? "ignore" : new TextEncoder().encode(options.stdin),
		stdout: "pipe",
		stderr: "pipe",
	});
	const timer = setTimeout(() => proc.kill("SIGKILL"), options.timeoutMs ?? 60_000);
	const [stdout, stderr] = await Promise.all([new Response(proc.stdout).text(), new Response(proc.stderr).text()]);
	await proc.exited;
	clearTimeout(timer);
	return { code: proc.exitCode, stdout, stderr, output: stdout + stderr };
}

/** `pi -p` with the fake model. */
export const print = (a: Agent, args: string[], options?: { stdin?: string; timeoutMs?: number }) =>
	run(a, ["-p", "--no-session", "--model", "fake/fake", ...args], options);

// ---- RPC mode ---------------------------------------------------------------------------------------------------

export class Rpc {
	readonly lines: any[] = [];
	private waiters: { test: (line: any) => boolean; resolve: (line: any) => void }[] = [];
	private n = 0;
	readonly proc: Bun.Subprocess<"pipe", "pipe", "pipe">;
	stderr = "";

	constructor(a: Agent, args: string[]) {
		this.proc = Bun.spawn([pi, "--mode", "rpc", ...args], { cwd: a.cwd, env: a.env, stdin: "pipe", stdout: "pipe", stderr: "pipe" });
		(async () => {
			let buffer = "";
			for await (const bytes of this.proc.stdout) {
				buffer += new TextDecoder().decode(bytes);
				let end: number;
				while ((end = buffer.indexOf("\n")) >= 0) {
					const text = buffer.slice(0, end);
					buffer = buffer.slice(end + 1);
					let line: any;
					try {
						line = JSON.parse(text);
					} catch {
						continue;
					}
					this.lines.push(line);
					const waiting = this.waiters.splice(0);
					for (const w of waiting) w.test(line) ? w.resolve(line) : this.waiters.push(w);
				}
			}
		})();
		(async () => {
			this.stderr = await new Response(this.proc.stderr).text();
		})();
	}

	/** The first line from index `since` on that passes `test`, or undefined after the timeout. */
	waitFor(test: (line: any) => boolean, since = 0, timeoutMs = 30_000): Promise<any> {
		const hit = this.lines.slice(since).find(test);
		if (hit) return Promise.resolve(hit);
		return Promise.race([
			new Promise<any>((resolve) => this.waiters.push({ test: (l) => this.lines.indexOf(l) >= since && test(l), resolve })),
			Bun.sleep(timeoutMs).then(() => undefined),
		]);
	}

	async send(command: object): Promise<any> {
		const id = `r${++this.n}`;
		const since = this.lines.length;
		this.write({ id, ...command });
		return this.waitFor((l) => l.type === "response" && l.id === id, since);
	}

	write(message: object) {
		this.proc.stdin.write(`${JSON.stringify(message)}\n`);
		this.proc.stdin.flush();
	}

	/** Sends a prompt and waits until Pi settles. */
	async prompt(message: string) {
		const since = this.lines.length;
		const response = await this.send({ type: "prompt", message });
		const settled = await this.waitFor((l) => l.type === "agent_settled", since);
		return { response, settled, since };
	}

	async close() {
		this.proc.kill();
		await this.proc.exited;
	}
}

// ---- Interactive mode (PTY) -------------------------------------------------------------------------------------

const ANSI = /\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[()][0-9A-Za-z]|\x1b[=>78DEHM]/g;

export class Tty {
	text = "";
	private decoder = new TextDecoder();
	readonly proc: Bun.Subprocess;

	constructor(a: Agent, args: string[]) {
		this.proc = Bun.spawn([pi, ...args], {
			cwd: a.cwd,
			env: a.env,
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

	get mark() {
		return this.text.length;
	}

	async waitFor(needle: string | RegExp, since = 0, timeoutMs = 20_000): Promise<boolean> {
		const deadline = performance.now() + timeoutMs;
		const found = () => (typeof needle === "string" ? this.text.includes(needle, since) : needle.test(this.text.slice(since)));
		while (!found()) {
			if (performance.now() > deadline || this.proc.exitCode !== null) return false;
			await Bun.sleep(20);
		}
		return true;
	}

	async settle(quietMs = 300, timeoutMs = 10_000) {
		const deadline = performance.now() + timeoutMs;
		let length = this.text.length;
		let last = performance.now();
		while (performance.now() < deadline && performance.now() - last < quietMs) {
			await Bun.sleep(20);
			if (this.text.length !== length) {
				length = this.text.length;
				last = performance.now();
			}
		}
	}

	send(data: string) {
		this.proc.terminal?.write(data);
	}

	/** Types a line into the editor and submits it. */
	async submit(line: string) {
		this.send(line);
		await Bun.sleep(150);
		this.send("\r");
	}

	async close() {
		this.proc.kill("SIGTERM");
		const timer = setTimeout(() => this.proc.kill("SIGKILL"), 3000);
		await this.proc.exited;
		clearTimeout(timer);
		this.proc.terminal?.close();
	}
}

// ---- Tests ------------------------------------------------------------------------------------------------------

type Test = { group: string; name: string; fn: () => Promise<void> };
export const tests: Test[] = [];
export const group = (name: string) => (test: string, fn: () => Promise<void>) => tests.push({ group: name, name: test, fn });

export class Failure extends Error {}
export function expect(condition: unknown, detail: string | (() => string)): asserts condition {
	if (!condition) throw new Failure(typeof detail === "string" ? detail : detail());
}
