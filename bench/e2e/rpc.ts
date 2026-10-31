import fs from "node:fs";
import path from "node:path";
import { agent, allRequests, expect, group, lastTurn, print, requests, Rpc, script } from "./harness.ts";

const test = group("rpc");

/** An RPC Pi with a saved session; `fn` gets it and the agent. */
async function withRpc(fn: (rpc: Rpc, a: ReturnType<typeof agent>) => Promise<void>, settings?: object, model = "fake/fake") {
	const a = agent({ settings });
	const rpc = new Rpc(a, ["--session-dir", path.join(a.home, "s"), "--model", model]);
	try {
		await fn(rpc, a);
	} finally {
		await rpc.close();
	}
}

const ok = (r: any, detail = "") => expect(r?.success, `${JSON.stringify(r) ?? "no response"} ${detail}`);

test("state, models, commands and thinking levels", async () => {
	await withRpc(async (rpc) => {
		const state = await rpc.send({ type: "get_state" });
		const models = await rpc.send({ type: "get_available_models" });
		ok(await rpc.send({ type: "get_commands" }));
		const levels = await rpc.send({ type: "get_available_thinking_levels" });
		ok(state);
		expect(JSON.stringify(models?.data).includes("fake2") && levels?.success, `${JSON.stringify(models)} ${JSON.stringify(levels)}`);
	});
});

test("prompt runs a tool; messages, bash, stats and last text", async () => {
	script({ name: "read", args: { path: "hello.txt" } }, { text: "LAST-MARK" });
	await withRpc(async (rpc) => {
		const { response, settled } = await rpc.prompt("go");
		ok(response);
		expect(settled, `not settled: ${rpc.lines.map((l) => l.type).join(",")} ${rpc.stderr}`);
		expect(JSON.stringify((await rpc.send({ type: "get_messages" }))?.data).includes("hello pitch"), "messages lack the tool result");
		expect(JSON.stringify((await rpc.send({ type: "bash", command: "echo rpc-bash" }))?.data).includes("rpc-bash"), "bash");
		ok(await rpc.send({ type: "get_session_stats" }));
		const last = await rpc.send({ type: "get_last_assistant_text" });
		expect(last?.data?.text === "LAST-MARK", JSON.stringify(last));
	});
});

test("models and thinking: set, cycle", async () => {
	script();
	await withRpc(async (rpc) => {
		ok(await rpc.send({ type: "set_model", provider: "fake", modelId: "think" }));
		ok(await rpc.send({ type: "set_thinking_level", level: "high" }));
		const cycled = await rpc.send({ type: "cycle_thinking_level" });
		ok(cycled);
		await rpc.prompt("hi");
		const used = requests.at(-1)?.body.model;
		const next = await rpc.send({ type: "cycle_model" });
		expect(used === "think" && next?.success, `${used} ${JSON.stringify(next)}`);
	});
});

test("session tree: name, entries, tree, fork, clone, switch", async () => {
	script();
	await withRpc(async (rpc) => {
		await rpc.prompt("ONE-MARK");
		await rpc.prompt("TWO-MARK");
		ok(await rpc.send({ type: "set_session_name", name: "rpc-name" }));
		const entries = await rpc.send({ type: "get_entries" });
		const tree = await rpc.send({ type: "get_tree" });
		const forkable = await rpc.send({ type: "get_fork_messages" });
		const first = forkable?.data?.messages?.find((m: any) => m.text === "ONE-MARK" || m.text?.includes("ONE-MARK"));
		expect(entries?.data?.entries?.length >= 4 && tree?.data?.tree?.length >= 1 && first, `${JSON.stringify(forkable)}`);
		const original = (await rpc.send({ type: "get_state" }))?.data?.sessionFile;
		ok(await rpc.send({ type: "clone" }));
		const clone = (await rpc.send({ type: "get_state" }))?.data?.sessionFile;
		expect(clone && clone !== original && fs.readFileSync(clone, "utf8").includes("TWO-MARK"), `clone ${clone}`);
		ok(await rpc.send({ type: "switch_session", sessionPath: original }));
		const fork = await rpc.send({ type: "fork", entryId: first.entryId });
		expect(fork?.data?.text?.includes("ONE-MARK"), JSON.stringify(fork));
		const switched = await rpc.send({ type: "switch_session", sessionPath: original });
		ok(switched);
		const messages = JSON.stringify((await rpc.send({ type: "get_messages" }))?.data);
		expect(messages.includes("TWO-MARK"), "switch_session did not restore the original");
	});
});

test("compact, export_html and new_session", async () => {
	script();
	await withRpc(
		async (rpc, a) => {
			await rpc.prompt("first");
			await rpc.prompt("second");
			const compact = await rpc.send({ type: "compact" });
			ok(compact);
			const html = path.join(a.home, "rpc.html");
			ok(await rpc.send({ type: "export_html", outputPath: html }));
			expect(fs.existsSync(html), "no html");
			ok(await rpc.send({ type: "new_session" }));
			const messages = (await rpc.send({ type: "get_messages" }))?.data?.messages;
			expect(messages?.length === 0, JSON.stringify(messages));
		},
		{ compaction: { keepRecentTokens: 1 } },
	);
});

test("auto-compaction near the context window", async () => {
	script({ text: "big", usage: 3500 });
	await withRpc(
		async (rpc) => {
			const { since } = await rpc.prompt("fill");
			const end = await rpc.waitFor((l) => l.type === "compaction_end", since);
			expect(end && !end.errorMessage, `${JSON.stringify(end)} ${rpc.lines.map((l) => l.type).join(",")}`);
		},
		{ compaction: { reserveTokens: 1000, keepRecentTokens: 1 } },
		"fake/small",
	);
});

test("auto-retry after an HTTP 500", async () => {
	const a = agent({ settings: { retry: { baseDelayMs: 10 } } });
	script({ status: 500 });
	const r = await print(a, ["--mode", "json", "hi"]);
	expect(r.stdout.includes("auto_retry_start") && r.stdout.includes('"success":true') && requests.length === 2, r.output.slice(-2000));
});

/** Starts a slow answer and waits for it to stream. */
async function streaming(rpc: Rpc, message: string) {
	const since = rpc.lines.length;
	ok(await rpc.send({ type: "prompt", message }));
	await rpc.waitFor((l) => l.type === "message_update", since);
	return since;
}

test("abort stops a streaming answer", async () => {
	script();
	await withRpc(async (rpc) => {
		const since = await streaming(rpc, "SLOW abort");
		ok(await rpc.send({ type: "abort" }));
		await rpc.waitFor((l) => l.type === "agent_settled", since);
		const last = (await rpc.send({ type: "get_messages" }))?.data?.messages?.at(-1);
		expect(last?.stopReason === "aborted" && !JSON.stringify(last).includes("word39"), JSON.stringify(last));
	});
});

for (const type of ["steer", "follow_up"]) {
	test(`${type} reaches the model`, async () => {
		script();
		await withRpc(async (rpc) => {
			ok(await rpc.send({ type: type === "steer" ? "set_steering_mode" : "set_follow_up_mode", mode: "all" }));
			const since = await streaming(rpc, `SLOW ${type}`);
			ok(await rpc.send({ type, message: `${type.toUpperCase()}-MARK` }));
			await rpc.waitFor((l) => l.type === "agent_settled", since);
			expect(allRequests().includes(`${type.toUpperCase()}-MARK`), lastTurn());
		});
	});
}
