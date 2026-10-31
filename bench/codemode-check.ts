// Runs `pi -p` against a fake OpenAI-compatible model that calls `codemode` once, and prints what the model saw.
// bun bench/codemode-check.ts /abs/pi   (the result should end with "Script completed ... 42")
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

const pi = process.argv[2];
const seen: { tools: string[]; toolResult?: string }[] = [];
const sse = (chunks: object[]) =>
	new Response(chunks.map((c) => `data: ${JSON.stringify(c)}\n\n`).join("") + "data: [DONE]\n\n", {
		headers: { "content-type": "text/event-stream" },
	});
const base = { id: "x", object: "chat.completion.chunk", created: 0, model: "fake" };

const server = Bun.serve({
	port: 0,
	async fetch(req) {
		const body = (await req.json()) as any;
		const toolMsg = body.messages.findLast((m: any) => m.role === "tool");
		seen.push({ tools: (body.tools ?? []).map((t: any) => t.function.name), toolResult: toolMsg && JSON.stringify(toolMsg.content) });
		if (!toolMsg) {
			return sse([
				{ ...base, choices: [{ index: 0, delta: { role: "assistant", tool_calls: [{ index: 0, id: "c1", type: "function", function: { name: "codemode", arguments: JSON.stringify({ code: "return 6 * 7;" }) } }] } }] },
				{ ...base, choices: [{ index: 0, delta: {}, finish_reason: "tool_calls" }] },
			]);
		}
		return sse([
			{ ...base, choices: [{ index: 0, delta: { role: "assistant", content: "Done." } }] },
			{ ...base, choices: [{ index: 0, delta: {}, finish_reason: "stop" }] },
		]);
	},
});

const home = fs.mkdtempSync(path.join(os.tmpdir(), "codemode-check-"));
fs.writeFileSync(path.join(home, "settings.json"), JSON.stringify({ defaultTools: ["+codemode"], codemode: { mode: "only" } }));
fs.writeFileSync(
	path.join(home, "models.json"),
	JSON.stringify({ providers: { fake: { baseUrl: `http://127.0.0.1:${server.port}/v1`, api: "openai-completions", apiKey: "fake", models: [{ id: "fake", name: "fake", contextWindow: 100000, maxTokens: 1000 }] } } }),
);
const proc = Bun.spawn([pi, "-p", "--no-session", "--model", "fake/fake", "go"], {
	env: { ...process.env, PI_CODING_AGENT_DIR: home, PI_SKIP_VERSION_CHECK: "1", PI_TELEMETRY: "0" },
	cwd: home,
	stdout: "pipe",
	stderr: "pipe",
});
const timer = setTimeout(() => proc.kill(), 60_000);
await proc.exited;
clearTimeout(timer);
server.stop(true);
console.log("exit", proc.exitCode, "| stdout:", (await new Response(proc.stdout).text()).trim().slice(0, 200), "| stderr:", (await new Response(proc.stderr).text()).trim().slice(0, 400));
for (const [i, s] of seen.entries()) console.log(`request ${i + 1}: tools=[${s.tools.join(",")}]`, s.toolResult ? `tool result=${s.toolResult.slice(0, 1200)}` : "");
