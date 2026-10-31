/**
 * End-to-end check of Pi's own capabilities against a Pi binary, with a local fake model:
 * print mode and the built-in tools, subcommands, codemode, MCP, skills/templates/extensions, sessions, RPC and the TUI.
 *
 *   bun bench/e2e/run.ts <pi> [leading args, e.g. bin/pitch pi]
 *
 * Run it on stock Pi first: what fails there is a wrong check, not a pitch bug.
 */
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

const pi = [path.resolve(process.argv[2] ?? ""), ...process.argv.slice(3)];
const fixtures = path.join(import.meta.dir, "fixtures");

// ---- Fake model: scripted steps in order, then "Done."; a user message containing SLOW gets a slow stream ----------

type Call = { name: string; args: object };
type Step = Call | Call[] | string;
let steps: Step[] = [];
let requests: { body: any; headers: Record<string, string> }[] = [];
function script(...s: Step[]) {
	steps = s;
	requests = [];
}
const seen = () => JSON.stringify(requests.map((r) => r.body));
const toolNames = (i: number) => (requests.at(i)?.body.tools ?? []).map((t: any) => t.function.name) as string[];
const systemPrompt = () => JSON.stringify(requests[0]?.body.messages.filter((m: any) => m.role === "system" || m.role === "developer"));

const chunk = (delta: object, finish_reason: string | null = null) =>
	`data: ${JSON.stringify({ id: "x", object: "chat.completion.chunk", created: 0, model: "fake", choices: [{ index: 0, delta, finish_reason }] })}\n\n`;
const sse = (body: string | ReadableStream) => new Response(body, { headers: { "content-type": "text/event-stream" } });

function slow() {
	let i = 0;
	let timer: Timer | undefined;
	return sse(
		new ReadableStream({
			start(controller) {
				const next = () => {
					if (i === 40) {
						controller.enqueue(new TextEncoder().encode(`${chunk({}, "stop")}data: [DONE]\n\n`));
						return controller.close();
					}
					controller.enqueue(new TextEncoder().encode(chunk({ role: "assistant", content: `word${i++} ` })));
					timer = setTimeout(next, 50);
				};
				next();
			},
			cancel: () => clearTimeout(timer),
		}),
	);
}

const server = Bun.serve({
	port: 0,
	idleTimeout: 120,
	async fetch(req) {
		const body = await req.json();
		requests.push({ body, headers: Object.fromEntries(req.headers) });
		if (JSON.stringify(body.messages.findLast((m: any) => m.role === "user")?.content ?? "").includes("SLOW")) return slow();
		const step = steps[requests.length - 1] ?? "Done.";
		if (typeof step === "string") return sse(`${chunk({ role: "assistant", content: step })}${chunk({}, "stop")}data: [DONE]\n\n`);
		const tool_calls = [step].flat().map((c, i) => ({
			index: i,
			id: `c${requests.length}_${i}`,
			type: "function",
			function: { name: c.name, arguments: JSON.stringify(c.args) },
		}));
		return sse(`${chunk({ role: "assistant", tool_calls })}${chunk({}, "tool_calls")}data: [DONE]\n\n`);
	},
});

// ---- Pi -----------------------------------------------------------------------------------------------------------

type Agent = { home: string; cwd: string; env: Record<string, string> };

function agent(options: { settings?: object; mcp?: object } = {}): Agent {
	const home = fs.mkdtempSync(path.join(os.tmpdir(), "pi-e2e-"));
	const cwd = path.join(home, "work");
	fs.mkdirSync(cwd);
	fs.writeFileSync(path.join(cwd, "hello.txt"), "hello pitch\nsecond line\nthird line\n");
	const model = { contextWindow: 100000, maxTokens: 1000, input: ["text", "image"] };
	const provider = { baseUrl: `http://127.0.0.1:${server.port}/v1`, api: "openai-completions", apiKey: "fake-key" };
	const models = ["fake", "fake2"].map((id) => ({ id, name: id, ...model }));
	fs.writeFileSync(path.join(home, "models.json"), JSON.stringify({ providers: { fake: { ...provider, models } } }));
	if (options.settings) fs.writeFileSync(path.join(home, "settings.json"), JSON.stringify(options.settings));
	if (options.mcp) fs.writeFileSync(path.join(home, "mcp.json"), JSON.stringify({ mcpServers: options.mcp }));
	const env = { ...process.env, HOME: home, PI_CODING_AGENT_DIR: home, PI_SKIP_VERSION_CHECK: "1", PI_TELEMETRY: "0", TERM: "xterm-256color" };
	return { home, cwd, env: env as Record<string, string> };
}

const mcpServer = (exposure?: string) => ({ command: "node", args: [path.join(fixtures, "mcp-echo-server.mjs")], ...(exposure && { exposure }) });

/** A PNG of the given size; 3000 is larger than the read tool sends, so it gets resized. */
function png(file: string, size: number) {
	fs.writeFileSync(file, Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==", "base64"));
	Bun.spawnSync(["sips", "-z", String(size), String(size), file]);
}

async function run(a: Agent, args: string[], stdin?: string) {
	const proc = Bun.spawn([...pi, ...args], { cwd: a.cwd, env: a.env, stdin: stdin === undefined ? "ignore" : new TextEncoder().encode(stdin), stdout: "pipe", stderr: "pipe" });
	const timer = setTimeout(() => proc.kill("SIGKILL"), 60_000);
	const [stdout, stderr] = await Promise.all([new Response(proc.stdout).text(), new Response(proc.stderr).text()]);
	await proc.exited;
	clearTimeout(timer);
	return { code: proc.exitCode, output: stdout + stderr };
}

async function waitUntil<T>(get: () => T, timeoutMs = 30_000): Promise<T | undefined> {
	const end = performance.now() + timeoutMs;
	for (let value = get(); performance.now() < end; value = get()) {
		if (value) return value;
		await Bun.sleep(20);
	}
}

class Rpc {
	lines: any[] = [];
	private n = 0;
	proc;
	constructor(a: Agent, args: string[]) {
		this.proc = Bun.spawn([...pi, "--mode", "rpc", ...args], { cwd: a.cwd, env: a.env, stdin: "pipe", stdout: "pipe", stderr: "inherit" });
		(async () => {
			let buffer = "";
			for await (const text of this.proc.stdout.pipeThrough(new TextDecoderStream())) {
				const parts = (buffer + text).split("\n");
				buffer = parts.pop()!;
				for (const part of parts) if (part.startsWith("{")) this.lines.push(JSON.parse(part));
			}
		})();
	}
	waitFor(test: (line: any) => boolean, since = 0) {
		return waitUntil(() => this.lines.slice(since).find(test));
	}
	write(message: object) {
		this.proc.stdin.write(`${JSON.stringify(message)}\n`);
		this.proc.stdin.flush();
	}
	/** Sends a command and returns the data of its successful response. */
	async send(command: { type: string; [key: string]: unknown }) {
		const id = `r${++this.n}`;
		const since = this.lines.length;
		this.write({ id, ...command });
		const response = await this.waitFor((l) => l.type === "response" && l.id === id, since);
		need(response?.success, `${command.type}: ${JSON.stringify(response)}`);
		return response.data;
	}
	async prompt(message: string) {
		const since = this.lines.length;
		await this.send({ type: "prompt", message });
		need(await this.waitFor((l) => l.type === "agent_settled", since), `"${message}" did not settle`);
	}
}

const ANSI = /\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[()][0-9A-Za-z]|\x1b[=>78DEHM]/g;

class Tty {
	text = "";
	proc;
	constructor(a: Agent, args: string[]) {
		const decoder = new TextDecoder();
		this.proc = Bun.spawn([...pi, ...args], {
			cwd: a.cwd,
			env: a.env,
			terminal: {
				cols: 160,
				rows: 48,
				data: (terminal, data) => {
					const text = decoder.decode(data, { stream: true });
					// Answer the background colour query, as a terminal does, so that Pi does not wait for it.
					if (text.includes("\x1b]11;?")) terminal.write("\x1b]11;rgb:0000/0000/0000\x1b\\");
					this.text += text.replace(ANSI, "");
				},
			},
		});
	}
	/** Waits for `needle` in the output after `since`, then for the screen to go quiet. */
	async expect(needle: string | RegExp, since: number, timeoutMs = 20_000) {
		const found = () => (typeof needle === "string" ? this.text.includes(needle, since) : needle.test(this.text.slice(since)));
		need(await waitUntil(found, timeoutMs), `no ${needle} in:\n${this.text.slice(since).slice(-1500)}`);
		for (let length = -1; length !== this.text.length; await Bun.sleep(300)) length = this.text.length;
	}
	/** Types a line, presses Enter and expects `needle` after it. */
	async submit(line: string, needle: string | RegExp) {
		const since = this.text.length;
		this.proc.terminal!.write(line);
		await Bun.sleep(150);
		this.proc.terminal!.write("\r");
		await this.expect(needle, since);
	}
}

// ---- Checks -------------------------------------------------------------------------------------------------------

function need(condition: unknown, detail: string): asserts condition {
	if (!condition) throw new Error(detail);
}
/** Fails with the markers missing from the requests the model got. */
function needSeen(...markers: (string | RegExp)[]) {
	const all = seen();
	const missing = markers.filter((m) => !(typeof m === "string" ? all.includes(m) : m.test(all)));
	need(missing.length === 0, `missing ${missing.join(" | ")} in:\n${all.slice(-3000)}`);
}

let failed = 0;
async function check(name: string, fn: () => Promise<void>) {
	const start = performance.now();
	try {
		await fn();
		console.log(`ok    ${name} (${Math.round(performance.now() - start)} ms)`);
	} catch (error) {
		failed++;
		console.log(`FAIL  ${name}\n      ${String((error as Error).message ?? error).slice(0, 3000).replaceAll("\n", "\n      ")}`);
	}
}

await check("print mode: inputs and built-in tools", async () => {
	const a = agent();
	fs.writeFileSync(path.join(a.cwd, "AGENTS.md"), "CONTEXT-MARK\n");
	png(path.join(a.cwd, "big.png"), 3000);
	script(
		{ name: "read", args: { path: "hello.txt", offset: 2, limit: 1 } },
		{ name: "read", args: { path: "nope.txt" } },
		{ name: "read", args: { path: "big.png" } },
		{ name: "write", args: { path: "sub/w.txt", content: "one\ntwo\n" } },
		{ name: "edit", args: { path: "sub/w.txt", edits: [{ oldText: "one", newText: "uno" }, { oldText: "two", newText: "dos" }] } },
		[
			{ name: "bash", args: { command: "echo ok-$((1+1)); exit 3" } },
			{ name: "grep", args: { pattern: "second", path: "." } },
			{ name: "find", args: { pattern: "*.txt" } },
			{ name: "ls", args: { path: "." } },
		],
	);
	const tools = "read,bash,edit,write,grep,find,ls";
	const r = await run(a, ["-p", "--no-session", "--model", "fake/fake", "--tools", tools, "--append-system-prompt", "APPEND-MARK", "--api-key", "KEY-MARK", "@hello.txt", "go"], "FROM-STDIN\n");
	need(r.code === 0 && r.output.includes("Done."), r.output);
	need(requests[0].headers.authorization === "Bearer KEY-MARK", "api key");
	need(fs.readFileSync(path.join(a.cwd, "sub/w.txt"), "utf8") === "uno\ndos\n", "write/edit");
	needSeen("hello pitch", "FROM-STDIN", "CONTEXT-MARK", "APPEND-MARK", "image_url", "second line", /ENOENT|not found/i, "original 3000x3000", "ok-2", "hello.txt:2");
});

await check("subcommands and info flags", async () => {
	const a = agent({ mcp: { t: mcpServer() } });
	for (const [args, want] of [
		[["--version"], /\d+\.\d+\.\d+/],
		[["--help"], /usage/i],
		[["--list-models"], /fake2/],
		[["list"], /No packages/],
		[["config", "--help"], /pi config/],
		[["mcp", "list"], /\bt\b/],
	] as const) {
		const r = await run(a, [...args]);
		need(r.code === 0 && want.test(r.output), `pi ${args.join(" ")}: ${r.output.slice(0, 500)}`);
	}
});

await check("codemode (only mode, with MCP)", async () => {
	const a = agent({ settings: { codemode: { mode: "only" } }, mcp: { t: mcpServer() } });
	png(path.join(a.cwd, "pic.png"), 64);
	const scripts = [
		'text("T-MARK"); console.log("C-MARK"); const f = await tools.read({ path: "hello.txt" }); const b = await tools.bash({ command: "echo B-$((2+3))" }); await tools.write({ path: "c.txt", content: "a" }); await tools.edit({ path: "c.txt", edits: [{ oldText: "a", newText: "EDITED" }] }); return [f.split("\\n")[0], b.output.trim(), await tools.read({ path: "c.txt" })].join("|");',
		'const t = Date.now(); await Promise.all([1, 2, 3].map((i) => tools.bash({ command: "sleep 0.5" }))); return "PARALLEL-" + (Date.now() - t < 1400);',
		'store("k", "STORED-MARK"); try { await tools.read({ path: "nope.txt" }); } catch { text("CAUGHT"); } image(await tools.read({ path: "pic.png" })); return [typeof process, typeof fetch, typeof setTimeout].join("|");',
		'const ns = await describeNamespace("mcp__t"); const s = await tools.mcp__t__add({ a: 2, b: 3 }); const found = await searchTools("run shell command"); const d = await describeTool("read"); return [load("k"), ns?.instructions, s.structuredContent.sum, found.some((t) => t.name === "bash"), ALL_TOOLS.length > 3, JSON.stringify(d ?? "").includes("path"), (await models.getModelsOfType("chat", "fake")).length].join("|");',
		'throw new Error("BOOM-MARK");',
		'// @options: {"timeout_ms": 1000}\nwhile (true) {}',
	];
	script(...scripts.map((code) => ({ name: "codemode", args: { code } })));
	const r = await run(a, ["-p", "--no-session", "--model", "fake/fake", "--tools", "+codemode", "go"]);
	// Compiled-in extensions may declare their own tools; the built-in ones must be hidden.
	need(toolNames(0).includes("codemode") && !toolNames(0).some((n) => ["read", "bash", "edit", "write"].includes(n)), `declared: ${toolNames(0)} ${r.output}`);
	needSeen("T-MARK", "C-MARK", "hello pitch|B-5|EDITED", "PARALLEL-true", "CAUGHT", "image_url", "undefined|undefined|undefined");
	needSeen("STORED-MARK|Echo server for tests.|5|true|true|true|2", "BOOM-MARK");
	need((seen().match(/Script failed/g)?.length ?? 0) >= 2, "error and timeout should fail their scripts");
});

await check("MCP: direct, resources, deferred with tool_search", async () => {
	const a = agent({ mcp: { t: mcpServer("direct"), d: mcpServer("deferred") } });
	script(
		{ name: "mcp__t__echo", args: { text: "hi" } },
		{ name: "list_mcp_resources", args: {} },
		{ name: "read_mcp_resource", args: { server: "t", uri: "test://note" } },
		{ name: "tool_search", args: { query: "add two numbers" } },
		{ name: "mcp__d__add", args: { a: 4, b: 5 } },
	);
	const r = await run(a, ["-p", "--no-session", "--model", "fake/fake", "go"]);
	need(toolNames(0).includes("mcp__t__echo") && !toolNames(0).includes("mcp__d__add") && toolNames(-1).includes("mcp__d__add"), `${toolNames(0)} | ${toolNames(-1)} ${r.output}`);
	need(JSON.stringify(requests.at(-1)?.body.messages.findLast((m: any) => m.role === "tool")).includes('"9"'), "deferred add");
	needSeen("echo: hi", "test://note", "MCP-RES");
});

await check("skill, prompt template and an extension from disk", async () => {
	const a = agent();
	script({ name: "e2e_tool", args: { text: "x" } });
	const args = ["--skill", path.join(fixtures, "skills/e2e-skill"), "--prompt-template", path.join(fixtures, "prompts"), "-e", path.join(fixtures, "extension.ts"), "--e2e-flag", "F"];
	const r = await run(a, ["-p", "--no-session", "--model", "fake/fake", ...args, "/e2e foo bar"]);
	need(systemPrompt().includes("SKILL-MARK") && systemPrompt().includes("EXT-SYSTEM-MARK"), `system prompt ${r.output}`);
	needSeen("TEMPLATE-MARK foo and foo bar", "EXT-TOOL:x:F");
});

await check("sessions: save, continue, fork, export", async () => {
	const a = agent();
	const dir = path.join(a.home, "s");
	const files = () => fs.readdirSync(dir, { recursive: true }).map(String).filter((f) => f.endsWith(".jsonl")).map((f) => path.join(dir, f));
	const turn = (...args: string[]) => run(a, ["-p", "--session-dir", dir, "--model", "fake/fake", ...args]);
	script();
	await turn("FIRST-MARK");
	script();
	await turn("-c", "second");
	needSeen("FIRST-MARK");
	const [file] = files();
	need(fs.readFileSync(file, "utf8").startsWith('{"type":"session"'), "session header");
	script();
	await turn("--fork", file, "forked");
	needSeen("FIRST-MARK");
	need(files().length === 2, `${files()}`);
	const html = path.join(a.home, "out.html");
	await run(a, ["--export", file, html]);
	need(/<html/i.test(fs.readFileSync(html, "utf8")), "export");
});

await check("rpc: commands, extension dialogs, compaction, fork, abort, steer, follow_up", async () => {
	const a = agent({ settings: { compaction: { keepRecentTokens: 1 } } });
	const rpc = new Rpc(a, ["--session-dir", path.join(a.home, "s"), "--model", "fake/fake", "-e", path.join(fixtures, "extension.ts")]);
	try {
		script({ name: "read", args: { path: "hello.txt" } }, "LAST-MARK");
		await rpc.send({ type: "get_state" });
		need(JSON.stringify(await rpc.send({ type: "get_available_models" })).includes("fake2"), "models");
		need(JSON.stringify(await rpc.send({ type: "get_commands" })).includes("e2e-ask"), "commands");
		await rpc.prompt("go");
		need(JSON.stringify(await rpc.send({ type: "get_messages" })).includes("hello pitch"), "tool result");
		need((await rpc.send({ type: "get_last_assistant_text" })).text === "LAST-MARK", "last text");
		need(JSON.stringify(await rpc.send({ type: "bash", command: "echo rpc-bash" })).includes("rpc-bash"), "bash");
		await rpc.send({ type: "get_session_stats" });
		await rpc.send({ type: "set_model", provider: "fake", modelId: "fake2" });
		await rpc.prompt("second");
		need(requests.at(-1)?.body.model === "fake2", "set_model");

		// An extension command asks through the client and then sends a user message.
		const since = rpc.lines.length;
		rpc.write({ id: "ask", type: "prompt", message: "/e2e-ask" });
		for (const [method, value] of [["input", { value: "VAL" }], ["select", { value: "beta" }], ["confirm", { confirmed: true }]] as const) {
			const ask = await rpc.waitFor((l) => l.type === "extension_ui_request" && l.method === method, since);
			need(ask, `no ${method} dialog`);
			rpc.write({ type: "extension_ui_response", id: ask.id, ...value });
		}
		need(await rpc.waitFor((l) => l.method === "notify" && l.message === "E2E-NOTIFY VAL beta true", since), "notify");
		await rpc.waitFor((l) => l.type === "agent_settled", since);
		needSeen("EXT-ASKED VAL beta true");

		await rpc.send({ type: "compact" });
		const html = path.join(a.home, "rpc.html");
		await rpc.send({ type: "export_html", outputPath: html });
		need(fs.existsSync(html), "export_html");
		await rpc.send({ type: "clone" });
		const first = (await rpc.send({ type: "get_fork_messages" })).messages[0];
		need((await rpc.send({ type: "fork", entryId: first.entryId })).text === "go", "fork");

		const streaming = async (message: string) => {
			const since = rpc.lines.length;
			await rpc.send({ type: "prompt", message });
			await rpc.waitFor((l) => l.type === "message_update", since);
			return since;
		};
		let start = await streaming("SLOW abort");
		await rpc.send({ type: "abort" });
		await rpc.waitFor((l) => l.type === "agent_settled", start);
		const last = (await rpc.send({ type: "get_messages" })).messages.at(-1);
		need(last.stopReason === "aborted" && !JSON.stringify(last).includes("word39"), `abort: ${JSON.stringify(last)}`);
		for (const type of ["steer", "follow_up"]) {
			start = await streaming(`SLOW ${type}`);
			await rpc.send({ type, message: `${type}-MARK` });
			await rpc.waitFor((l) => l.type === "agent_settled", start);
			needSeen(`${type}-MARK`);
		}
		await rpc.send({ type: "new_session" });
		need((await rpc.send({ type: "get_messages" })).messages.length === 0, "new_session");
	} finally {
		rpc.proc.kill();
	}
});

await check("tui: prompt, !bash, /model, /export, /copy, escape, /quit", async () => {
	const a = agent();
	script({ name: "bash", args: { command: "echo TTY-BASH" } }, "TTY-ANSWER");
	const tty = new Tty(a, ["--session-dir", path.join(a.home, "s"), "--model", "fake/fake"]);
	try {
		await tty.expect("fake", 0);
		await tty.submit("go", "TTY-ANSWER");
		needSeen("TTY-BASH");
		await tty.submit("!echo BANG-$((6*7))", "BANG-42");
		// The first Enter takes the autocompletion (fake/fake2), the second runs the command.
		await tty.submit("/model fake2", "fake/fake2");
		await tty.submit("", "Model: fake2");
		await tty.submit("/copy", /[Cc]opied/);
		need(Bun.spawnSync(["pbpaste"]).stdout.toString().includes("TTY-ANSWER"), "clipboard");
		const html = path.join(a.home, "tty.html");
		await tty.submit(`/export ${html}`, "tty.html");
		need(fs.existsSync(html), "export");
		const since = tty.text.length;
		tty.proc.terminal!.write("SLOW please\r");
		need(await waitUntil(() => tty.text.includes("word3", since)), "no stream");
		tty.proc.terminal!.write("\x1b");
		await tty.expect(/[Aa]bort/, since);
		await tty.submit("/quit", "");
		need(await waitUntil(() => tty.proc.exitCode === 0, 5000), `exit ${tty.proc.exitCode}`);
	} finally {
		tty.proc.kill("SIGKILL");
		tty.proc.terminal?.close();
	}
});

console.log(failed ? `\n${failed} failed` : "\nall passed");
server.stop(true);
process.exit(failed ? 1 : 0);
