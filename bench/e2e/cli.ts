import fs from "node:fs";
import path from "node:path";
import { agent, allRequests, expect, group, mcpEcho, png, print, requests, run, script, systemPrompt, toolNames } from "./harness.ts";

const test = group("cli");

test("print mode answers", async () => {
	const a = agent();
	script();
	const r = await print(a, ["hi"]);
	expect(r.code === 0 && r.stdout.includes("Done."), r.output);
});

test("json mode streams events", async () => {
	const a = agent();
	script();
	const r = await print(a, ["--mode", "json", "hi"]);
	const types = r.stdout.trim().split("\n").map((l) => JSON.parse(l).type);
	expect(r.code === 0 && types.includes("agent_end") && types.includes("message_update"), r.output.slice(0, 2000));
});

test("@file attaches a text file", async () => {
	const a = agent();
	script();
	await print(a, ["@hello.txt", "what is in it"]);
	expect(allRequests().includes("hello pitch"), allRequests().slice(0, 2000));
});

test("@file attaches an image", async () => {
	const a = agent();
	png(path.join(a.cwd, "pic.png"), 64);
	script();
	await print(a, ["@pic.png", "describe"]);
	expect(allRequests().includes("image_url"), allRequests().slice(0, 2000));
});

test("piped stdin joins the prompt", async () => {
	const a = agent();
	script();
	await print(a, ["summarize"], { stdin: "FROM-STDIN\n" });
	expect(allRequests().includes("FROM-STDIN"), allRequests().slice(0, 2000));
});

test("--system-prompt and --append-system-prompt", async () => {
	const a = agent();
	script();
	await print(a, ["--system-prompt", "SYS-MARK", "--append-system-prompt", "APPEND-MARK", "hi"]);
	const s = systemPrompt();
	expect(s.includes("SYS-MARK") && s.includes("APPEND-MARK"), s.slice(0, 2000));
});

test("AGENTS.md is loaded, --no-context-files skips it", async () => {
	const a = agent();
	fs.writeFileSync(path.join(a.cwd, "AGENTS.md"), "CONTEXT-MARK\n");
	script();
	await print(a, ["--approve", "hi"]);
	const loaded = systemPrompt().includes("CONTEXT-MARK");
	script();
	await print(a, ["--approve", "--no-context-files", "hi"]);
	expect(loaded && !systemPrompt().includes("CONTEXT-MARK"), `loaded=${loaded}`);
});

test("--no-tools, --tools and --exclude-tools", async () => {
	const a = agent();
	script();
	await print(a, ["--no-tools", "hi"]);
	const none = toolNames(0);
	script();
	await print(a, ["--tools", "read,grep", "hi"]);
	const some = toolNames(0).sort().join(",");
	script();
	await print(a, ["--exclude-tools", "bash", "hi"]);
	const excluded = toolNames(0);
	expect(none.length === 0 && some === "grep,read" && !excluded.includes("bash") && excluded.includes("read"), `${none} | ${some} | ${excluded}`);
});

test("--api-key reaches the provider", async () => {
	const a = agent();
	script();
	await print(a, ["--api-key", "KEY-MARK", "hi"]);
	expect(requests[0]?.headers.authorization === "Bearer KEY-MARK", JSON.stringify(requests[0]?.headers));
});

test("--provider with --model, and --thinking", async () => {
	const a = agent();
	script();
	const r = await run(a, ["-p", "--no-session", "--provider", "fake", "--model", "think", "--thinking", "high", "hi"]);
	expect(r.code === 0 && requests[0]?.body.model === "think", `${r.output} ${requests[0]?.body.model}`);
});

test("--offline runs", async () => {
	const a = agent();
	script();
	const r = await print(a, ["--offline", "hi"]);
	expect(r.code === 0 && r.stdout.includes("Done."), r.output);
});

test("--version, --help, --list-models", async () => {
	const a = agent();
	const v = await run(a, ["--version"]);
	const h = await run(a, ["--help"]);
	const m = await run(a, ["--list-models", "small"]);
	expect(/\d+\.\d+\.\d+/.test(v.output) && /usage/i.test(h.output) && m.output.includes("small") && !/\bthink\b/.test(m.output), `${v.output}\n${m.output}`);
});

test("subcommands: list, config --help, mcp list", async () => {
	const a = agent({ mcp: mcpEcho() });
	const outputs = [await run(a, ["list"]), await run(a, ["config", "--help"]), await run(a, ["mcp", "list"])];
	expect(
		outputs.every((o) => o.code === 0 && !o.output.includes("No API key")) && /\bt\b/.test(outputs[2].output),
		outputs.map((o) => `${o.code}: ${o.output.slice(0, 300)}`).join("\n"),
	);
});
