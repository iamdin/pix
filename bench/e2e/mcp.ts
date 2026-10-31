import { agent, expect, group, lastTurn, mcpEcho, print, script, toolNames } from "./harness.ts";

const test = group("mcp");

test("direct exposure declares and calls the tool", async () => {
	const a = agent({ mcp: mcpEcho({ exposure: "direct" }) });
	script({ name: "mcp__t__echo", args: { text: "hi" } });
	const r = await print(a, ["go"]);
	expect(toolNames(0).includes("mcp__t__add") && lastTurn().includes("echo: hi"), `${toolNames(0)} ${lastTurn()} ${r.output}`);
});

test("deferred exposure: tool_search loads the tool", async () => {
	const a = agent({ mcp: mcpEcho({ exposure: "deferred" }) });
	script({ name: "tool_search", args: { query: "add two numbers" } }, { name: "mcp__t__add", args: { a: 4, b: 5 } });
	const r = await print(a, ["go"]);
	expect(!toolNames(0).includes("mcp__t__add") && toolNames(1).includes("mcp__t__add") && lastTurn().includes("9"), `${toolNames(0)} | ${toolNames(1)} | ${lastTurn()} ${r.output}`);
});

test("resources: list and read", async () => {
	const a = agent({ mcp: mcpEcho({ exposure: "direct" }) });
	script({ name: "list_mcp_resources", args: {} }, { name: "read_mcp_resource", args: { server: "t", uri: "test://note" } });
	const r = await print(a, ["go"]);
	expect(lastTurn().includes("test://note") && lastTurn().includes("MCP-RES"), `${lastTurn()} ${r.output}`);
});

test("--no-mcp turns MCP off", async () => {
	const a = agent({ mcp: mcpEcho({ exposure: "direct" }) });
	script();
	await print(a, ["--no-mcp", "go"]);
	expect(!toolNames(0).some((n) => n.startsWith("mcp__")), toolNames(0).join(","));
});
