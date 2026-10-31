/**
 * Check Pi's own capabilities in a built binary, end to end, against a local fake model that makes scripted tool calls.
 *
 *   bun bench/pi-capabilities.ts /abs/pi
 *
 * Each scenario runs `pi -p` in a fresh agent directory and checks what Pi sent back to the model. Exit status 1 if any
 * scenario fails. The interactive TUI is covered by bench/run.ts (tuiwrite, md).
 */
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

const pi = path.resolve(process.argv[2] ?? "");
if (!fs.existsSync(pi)) throw new Error("usage: bun bench/pi-capabilities.ts /abs/pi");

type Call = { name: string; args: Record<string, unknown> };
type Scenario = { name: string; calls: Call[]; expect: (string | RegExp)[]; args?: string[]; setup?: (home: string, cwd: string) => void };

let calls: Call[] = [];
let requests: any[] = [];
const sse = (chunks: object[]) =>
	new Response(`${chunks.map((c) => `data: ${JSON.stringify(c)}\n\n`).join("")}data: [DONE]\n\n`, {
		headers: { "content-type": "text/event-stream" },
	});
const chunk = (delta: object, finish_reason: string | null = null) => ({
	id: "x",
	object: "chat.completion.chunk",
	created: 0,
	model: "fake",
	choices: [{ index: 0, delta, finish_reason }],
});
// An answer of 40 chunks, 50 ms apart: long enough to abort or steer while it streams.
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
const server = Bun.serve({
	port: 0,
	async fetch(req) {
		const body = await req.json();
		requests.push(body);
		if (JSON.stringify(body.messages.findLast((m: any) => m.role === "user")?.content).includes("SLOW")) return slowAnswer();
		const call = calls[requests.length - 1];
		if (!call) return sse([chunk({ role: "assistant", content: "Done." }), chunk({}, "stop")]);
		const tool_calls = [{ index: 0, id: `c${requests.length}`, type: "function", function: { name: call.name, arguments: JSON.stringify(call.args) } }];
		return sse([chunk({ role: "assistant", tool_calls }), chunk({}, "tool_calls")]);
	},
});

const mcpServer = path.join(import.meta.dir, "mcp-echo-server.mjs");
const scenarios: Scenario[] = [
	{ name: "read", calls: [{ name: "read", args: { path: "hello.txt" } }], expect: ["hello pitch"] },
	{
		name: "write+edit",
		calls: [
			{ name: "write", args: { path: "w.txt", content: "one\n" } },
			{ name: "edit", args: { path: "w.txt", edits: [{ oldText: "one", newText: "two" }] } },
			{ name: "read", args: { path: "w.txt" } },
		],
		expect: ["two"],
	},
	{ name: "bash", calls: [{ name: "bash", args: { command: "echo ok-$((1+1))" } }], expect: ["ok-2"] },
	{
		name: "grep+find+ls",
		args: ["--tools", "grep,find,ls"],
		calls: [
			{ name: "grep", args: { pattern: "hello", path: "." } },
			{ name: "find", args: { pattern: "*.txt" } },
			{ name: "ls", args: { path: "." } },
		],
		expect: [/hello\.txt/],
	},
	{
		name: "image read (resize worker, photon)",
		calls: [{ name: "read", args: { path: "big.png" } }],
		expect: [/"type":"image_url"|image\/png|image\/jpeg/],
		setup: (_home, cwd) => {
			// A 3000×3000 PNG, larger than the read tool sends, so it is resized.
			const png = path.join(cwd, "big.png");
			fs.writeFileSync(png, Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==", "base64"));
			Bun.spawnSync(["sips", "-z", "3000", "3000", png]);
		},
	},
	{
		name: "codemode with a nested tool call",
		args: ["--tools", "codemode,read"],
		calls: [{ name: "codemode", args: { code: 'const r = await tools.read({ path: "hello.txt" }); text(r);' } }],
		expect: ["Script completed", "hello pitch"],
	},
	{
		name: "mcp (stdio server)",
		calls: [{ name: "mcp__t__echo", args: { text: "hi" } }],
		expect: ["echo: hi"],
		setup: (home) =>
			fs.writeFileSync(path.join(home, "mcp.json"), JSON.stringify({ mcpServers: { t: { command: "node", args: [mcpServer], exposure: "direct" } } })),
	},
];

let failed = 0;
function agentDir() {
	const home = fs.mkdtempSync(path.join(os.tmpdir(), "pi-capabilities-"));
	const cwd = path.join(home, "work");
	fs.mkdirSync(cwd);
	fs.writeFileSync(path.join(cwd, "hello.txt"), "hello pitch\n");
	const model = { id: "fake", name: "fake", contextWindow: 100000, maxTokens: 1000, input: ["text", "image"] };
	fs.writeFileSync(
		path.join(home, "models.json"),
		JSON.stringify({ providers: { fake: { baseUrl: `http://127.0.0.1:${server.port}/v1`, api: "openai-completions", apiKey: "fake", models: [model] } } }),
	);
	return { home, cwd };
}
async function runPi(home: string, cwd: string, args: string[]) {
	const proc = Bun.spawn([pi, ...args], {
		cwd,
		env: { ...process.env, PI_CODING_AGENT_DIR: home, PI_SKIP_VERSION_CHECK: "1", PI_TELEMETRY: "0" },
		stdout: "pipe",
		stderr: "pipe",
	});
	const timer = setTimeout(() => proc.kill(), 60_000);
	const [stdout, stderr] = await Promise.all([new Response(proc.stdout).text(), new Response(proc.stderr).text()]);
	await proc.exited;
	clearTimeout(timer);
	return { code: proc.exitCode, stdout, stderr };
}
function report(name: string, ok: boolean, detail: string) {
	if (!ok) failed++;
	console.log(`${ok ? "ok  " : "FAIL"} ${name}${ok ? "" : `\n     ${detail.slice(0, 1500).replaceAll("\n", "\n     ")}`}`);
}

for (const scenario of scenarios) {
	const { home, cwd } = agentDir();
	scenario.setup?.(home, cwd);
	calls = scenario.calls;
	requests = [];
	const result = await runPi(home, cwd, ["-p", "--no-session", "--model", "fake/fake", ...(scenario.args ?? []), "go"]);
	const toolResults = JSON.stringify(requests.at(-1)?.messages?.filter((m: any) => m.role === "tool" || m.role === "user") ?? []);
	const missing = scenario.expect.filter((e) => !(typeof e === "string" ? toolResults.includes(e) : e.test(toolResults)));
	report(scenario.name, result.code === 0 && missing.length === 0 && requests.length === scenario.calls.length + 1, `exit ${result.code}, requests ${requests.length}, missing ${missing.join(" | ")}\n${result.stderr}\n${toolResults}`);
}

// Sessions: a second run continues the first, and the session exports to HTML.
{
	const { home, cwd } = agentDir();
	const sessions = path.join(home, "sessions-test");
	calls = [];
	requests = [];
	await runPi(home, cwd, ["-p", "--session-dir", sessions, "--model", "fake/fake", "first message"]);
	requests = [];
	const second = await runPi(home, cwd, ["-p", "-c", "--session-dir", sessions, "--model", "fake/fake", "second message"]);
	report("session continue (-c)", second.code === 0 && JSON.stringify(requests[0]?.messages ?? []).includes("first message"), `${second.stderr}`);
	const file = fs.readdirSync(sessions, { recursive: true }).map(String).find((f) => f.endsWith(".jsonl"));
	const html = path.join(home, "out.html");
	const exported = file ? await runPi(home, cwd, ["--export", path.join(sessions, file), html]) : undefined;
	report("export html", !!exported && fs.existsSync(html) && /<html/i.test(fs.readFileSync(html, "utf8")), `${file} ${exported?.stdout} ${exported?.stderr}`);
}

// RPC mode: commands answer, a prompt runs a tool, and the session can be inspected, compacted, exported and replaced.
{
	const { home, cwd } = agentDir();
	calls = [{ name: "read", args: { path: "hello.txt" } }];
	requests = [];
	// Keep almost nothing on compaction, so a short session has something to summarize.
	fs.writeFileSync(path.join(home, "settings.json"), JSON.stringify({ compaction: { keepRecentTokens: 1 } }));
	const proc = Bun.spawn([pi, "--mode", "rpc", "--session-dir", path.join(home, "sessions-rpc"), "--model", "fake/fake"], {
		cwd,
		env: { ...process.env, PI_CODING_AGENT_DIR: home, PI_SKIP_VERSION_CHECK: "1", PI_TELEMETRY: "0" },
		stdin: "pipe",
		stdout: "pipe",
		stderr: "pipe",
	});
	const lines: any[] = [];
	const waiters: { test: (line: any) => boolean; resolve: (line: any) => void }[] = [];
	(async () => {
		let buffer = "";
		for await (const bytes of proc.stdout) {
			buffer += new TextDecoder().decode(bytes);
			let end: number;
			while ((end = buffer.indexOf("\n")) >= 0) {
				const line = JSON.parse(buffer.slice(0, end));
				buffer = buffer.slice(end + 1);
				lines.push(line);
				for (const w of waiters.splice(0).filter((w) => !w.test(line) || (w.resolve(line), false))) waiters.push(w);
			}
		}
	})();
	const waitFor = (test: (line: any) => boolean) =>
		Promise.race([
			new Promise<any>((resolve) => {
				const hit = lines.find(test);
				hit ? resolve(hit) : waiters.push({ test, resolve });
			}),
			Bun.sleep(30_000).then(() => undefined),
		]);
	let n = 0;
	const send = async (command: object) => {
		const id = `r${++n}`;
		proc.stdin.write(`${JSON.stringify({ id, ...command })}\n`);
		proc.stdin.flush();
		return waitFor((line) => line.type === "response" && line.id === id);
	};
	const check = async (name: string, command: object, ok: (response: any) => boolean) => {
		const response = await send(command);
		report(`rpc ${name}`, !!response?.success && ok(response), JSON.stringify(response) ?? "no response");
		return response;
	};
	await check("get_state", { type: "get_state" }, () => true);
	await check("get_available_models", { type: "get_available_models" }, (r) => JSON.stringify(r.data).includes("fake"));
	await check("get_commands", { type: "get_commands" }, () => true);
	await check("prompt", { type: "prompt", message: "go" }, () => true);
	const settled = await waitFor((line) => line.type === "agent_settled");
	report("rpc prompt runs a tool to the end", !!settled, `events: ${lines.map((l) => l.type).join(",")}`);
	await check("get_messages", { type: "get_messages" }, (r) => JSON.stringify(r.data).includes("hello pitch"));
	await check("bash", { type: "bash", command: "echo rpc-bash" }, (r) => JSON.stringify(r.data).includes("rpc-bash"));
	await check("get_session_stats", { type: "get_session_stats" }, () => true);
	await check("second prompt", { type: "prompt", message: "again" }, () => true);
	await waitFor((line) => line.type === "agent_settled" && line !== settled);
	await check("compact", { type: "compact" }, () => true);
	const html = path.join(home, "rpc.html");
	await check("export_html", { type: "export_html", outputPath: html }, () => fs.existsSync(html));
	await check("new_session", { type: "new_session" }, () => true);

	// Interrupting a streaming answer: abort stops it, steer and follow_up add a message the model then gets.
	const streaming = async (message: string) => {
		const start = lines.length;
		await send({ type: "prompt", message });
		await waitFor((line) => lines.indexOf(line) >= start && line.type === "message_update");
		return start;
	};
	const settledAfter = (start: number) => waitFor((line) => lines.indexOf(line) >= start && line.type === "agent_settled");
	let start = await streaming("SLOW abort test");
	await check("abort", { type: "abort" }, () => true);
	await settledAfter(start);
	const afterAbort = await send({ type: "get_messages" });
	const last = afterAbort?.data?.messages?.at(-1);
	report("rpc abort stops the answer", last?.role === "assistant" && last?.stopReason === "aborted" && !JSON.stringify(last).includes("word39"), JSON.stringify(last));
	for (const [type, mark] of [["steer", "STEER-MARK"], ["follow_up", "FOLLOW-MARK"]]) {
		const before = requests.length;
		start = await streaming(`SLOW ${type} test`);
		await check(type, { type, message: mark }, () => true);
		await settledAfter(start);
		report(`rpc ${type} reaches the model`, requests.slice(before).some((r) => JSON.stringify(r.messages).includes(mark)), `${requests.length - before} requests`);
	}
	proc.kill();
	await proc.exited;
}

// Commands that only print.
{
	const { home, cwd } = agentDir();
	fs.writeFileSync(path.join(home, "mcp.json"), JSON.stringify({ mcpServers: { t: { command: "node", args: [mcpServer] } } }));
	for (const [args, want] of [
		[["--version"], /\d+\.\d+\.\d+/],
		[["--help"], /Usage/],
		[["--list-models"], /fake/],
		[["list"], /package|No/i],
		[["config", "--help"], /config/i],
		[["mcp", "list"], /\bt\b/],
	] as [string[], RegExp][]) {
		const result = await runPi(home, cwd, args);
		const output = result.stdout + result.stderr;
		report(`pi ${args.join(" ")}`, result.code === 0 && want.test(output) && !output.includes("No API key"), `exit ${result.code} ${output}`);
	}
	report("native clipboard helper shipped", fs.existsSync(path.join(path.dirname(pi), "native/darwin/prebuilds/darwin-arm64/darwin-platform.node")), "missing");
}

server.stop(true);
console.log(failed ? `${failed} failed` : "all passed");
process.exit(failed ? 1 : 0);
