import path from "node:path";
import { agent, expect, group, lastTurn, mcpEcho, png, print, script, toolNames } from "./harness.ts";

const test = group("codemode");

/** Runs scripts as successive codemode calls and returns the last tool result. */
async function codemode(scripts: string[], options: { args?: string[]; settings?: object; mcp?: object; setup?: (cwd: string) => void } = {}) {
	const a = agent({ settings: options.settings, mcp: options.mcp });
	options.setup?.(a.cwd);
	script(...scripts.map((code) => ({ name: "codemode", args: { code } })));
	const r = await print(a, [...(options.args ?? ["--tools", "+codemode"]), "go"]);
	return { turn: lastTurn(), output: r.output };
}

test("return, text and console.log", async () => {
	const { turn } = await codemode(['text("T-MARK"); console.log("C-MARK", 1); return { r: "R-MARK" };']);
	expect(turn.includes("Script completed") && turn.includes("T-MARK") && turn.includes("C-MARK 1") && turn.includes("R-MARK"), turn);
});

test("nested read and bash calls", async () => {
	const { turn } = await codemode([
		'const f = await tools.read({ path: "hello.txt" }); const b = await tools.bash({ command: "echo B-$((2+3)); exit 4" }); return [f.split("\\n")[0], b.output.trim(), b.exit_code];',
	]);
	expect(turn.includes("hello pitch") && turn.includes("B-5") && turn.includes("4]"), turn);
});

test("write and edit through scripts", async () => {
	const { turn } = await codemode([
		'await tools.write({ path: "c.txt", content: "a\\n" }); await tools.edit({ path: "c.txt", edits: [{ oldText: "a", newText: "EDITED" }] }); return await tools.read({ path: "c.txt" });',
	]);
	expect(turn.includes("EDITED"), turn);
});

test("Promise.all runs calls in parallel", async () => {
	const { turn } = await codemode([
		'const t = Date.now(); const r = await Promise.all([1, 2, 3].map((i) => tools.bash({ command: `sleep 0.5; echo P${i}` }))); return [r.map((x) => x.output.trim()).join(","), Date.now() - t < 1400];',
	]);
	expect(turn.includes("P1,P2,P3") && turn.includes("true"), turn);
});

test("a thrown error fails the script and keeps output", async () => {
	const { turn } = await codemode(['text("BEFORE"); throw new Error("BOOM-MARK");']);
	expect(turn.includes("Script failed") && turn.includes("BEFORE") && turn.includes("BOOM-MARK"), turn);
});

test("a failing tool call rejects", async () => {
	const { turn } = await codemode(['try { await tools.read({ path: "nope.txt" }); } catch (e) { return "CAUGHT " + e.message; }']);
	expect(turn.includes("CAUGHT") && /ENOENT|not found|does not exist/i.test(turn), turn);
});

test("store and load across calls", async () => {
	const { turn } = await codemode(['store("k", { v: "STORED-MARK" }); return "saved";', 'return load("k").v;']);
	expect(turn.includes("STORED-MARK"), turn);
});

test("ALL_TOOLS, searchTools and describeTool", async () => {
	const { turn } = await codemode([
		'const names = ALL_TOOLS.map((t) => t.name); const found = await searchTools("run shell command"); const d = await describeTool("read"); return [names.includes("bash"), found.map((t) => t.name).includes("bash"), JSON.stringify(d ?? "").includes("path")];',
	]);
	expect(turn.includes("[true,true,true]"), turn);
});

test("image() shows an image from read", async () => {
	const { turn } = await codemode(['image(await tools.read({ path: "pic.png" })); return "IMG";'], {
		setup: (cwd) => png(path.join(cwd, "pic.png"), 64),
	});
	expect(turn.includes("IMG") && turn.includes("image_url"), turn.slice(0, 2000));
});

test("timeout_ms stops an endless script", async () => {
	const { turn } = await codemode(['// @options: {"timeout_ms": 1000}\nwhile (true) {}']);
	expect(turn.includes("Script failed") && /time/i.test(turn), turn);
});

test("the sandbox has no Node or network", async () => {
	const { turn } = await codemode(['return [typeof process, typeof require, typeof fetch, typeof setTimeout].join(",");']);
	expect(turn.includes("undefined,undefined,undefined,undefined"), turn);
});

test('codemode.mode "only" declares only codemode', async () => {
	const a = agent({ settings: { codemode: { mode: "only" } } });
	script({ name: "codemode", args: { code: 'return await tools.bash({ command: "echo ONLY-MARK" });' } });
	await print(a, ["--tools", "+codemode", "go"]);
	expect(toolNames(0).join(",") === "codemode" && lastTurn().includes("ONLY-MARK"), `${toolNames(0)} ${lastTurn()}`);
});

test("MCP tools through codemode (default exposure)", async () => {
	const { turn, output } = await codemode(
		[
			'const ns = await describeNamespace("mcp__t"); const e = await tools.mcp__t__echo({ text: "hi" }); const s = await tools.mcp__t__add({ a: 2, b: 3 }); return [ns?.instructions, e.content[0].text, s.structuredContent.sum];',
		],
		{ args: [], mcp: mcpEcho() },
	);
	expect(turn.includes("Echo server for tests.") && turn.includes("echo: hi") && turn.includes(",5]"), `${turn}\n${output}`);
});

test("models.getModelsOfType lists the catalog", async () => {
	const { turn } = await codemode(['return (await models.getModelsOfType("chat", "fake")).map((m) => m.id).sort().join(",");']);
	expect(turn.includes("fake,fake2,small,think"), turn);
});
