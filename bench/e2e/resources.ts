import path from "node:path";
import { agent, allRequests, expect, fixtures, group, lastTurn, print, Rpc, script, systemPrompt, toolNames } from "./harness.ts";

const test = group("resources");
const extension = path.join(fixtures, "extension.ts");

test("--skill is listed in the system prompt", async () => {
	const a = agent();
	script();
	await print(a, ["--skill", path.join(fixtures, "skills/e2e-skill"), "hi"]);
	expect(systemPrompt().includes("SKILL-MARK"), systemPrompt().slice(0, 3000));
});

test("/skill:name expands the skill", async () => {
	const a = agent();
	script();
	await print(a, ["--skill", path.join(fixtures, "skills/e2e-skill"), "/skill:e2e-skill now"]);
	expect(allRequests().includes("SKILL-BODY"), allRequests().slice(0, 3000));
});

test("a prompt template expands its arguments", async () => {
	const a = agent();
	script();
	await print(a, ["--prompt-template", path.join(fixtures, "prompts"), "/e2e foo bar"]);
	expect(allRequests().includes("TEMPLATE-MARK foo and foo bar"), allRequests().slice(0, 3000));
});

test("an extension from disk: tool, flag and system prompt hook", async () => {
	const a = agent();
	script({ name: "e2e_tool", args: { text: "x" } });
	const r = await print(a, ["-e", extension, "--e2e-flag", "F", "go"]);
	expect(
		toolNames(0).includes("e2e_tool") && lastTurn().includes("EXT-TOOL:x:F") && systemPrompt().includes("EXT-SYSTEM-MARK"),
		`${toolNames(0)} ${lastTurn()} ${r.output}`,
	);
});

test("an extension command with dialogs over RPC", async () => {
	const a = agent();
	script();
	const rpc = new Rpc(a, ["--no-session", "--model", "fake/fake", "-e", extension]);
	try {
		const since = rpc.lines.length;
		rpc.write({ id: "c1", type: "prompt", message: "/e2e-ask" });
		for (const [method, value] of [
			["input", { value: "VAL" }],
			["select", { value: "beta" }],
			["confirm", { confirmed: true }],
		] as const) {
			const ask = await rpc.waitFor((l) => l.type === "extension_ui_request" && l.method === method, since);
			expect(ask, `no ${method} request: ${rpc.lines.map((l) => l.type).join(",")} ${rpc.stderr}`);
			rpc.write({ type: "extension_ui_response", id: ask.id, ...value });
		}
		const notify = await rpc.waitFor((l) => l.type === "extension_ui_request" && l.method === "notify", since);
		await rpc.waitFor((l) => l.type === "agent_settled", since);
		expect(notify?.message === "E2E-NOTIFY VAL beta true" && allRequests().includes("EXT-ASKED VAL beta true"), `${JSON.stringify(notify)} ${allRequests().slice(0, 1000)}`);
	} finally {
		await rpc.close();
	}
});
