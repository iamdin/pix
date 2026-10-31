// A minimal MCP stdio server for the end-to-end tests: tools `echo` and `add`, and one resource.
import readline from "node:readline";

const send = (message) => process.stdout.write(`${JSON.stringify({ jsonrpc: "2.0", ...message })}\n`);
const tools = [
	{ name: "echo", description: "Echo the text.", inputSchema: { type: "object", properties: { text: { type: "string" } }, required: ["text"] } },
	{
		name: "add",
		description: "Add two numbers.",
		inputSchema: { type: "object", properties: { a: { type: "number" }, b: { type: "number" } }, required: ["a", "b"] },
	},
];
for await (const line of readline.createInterface({ input: process.stdin })) {
	const { id, method, params } = JSON.parse(line);
	if (id === undefined) continue;
	if (method === "initialize") {
		send({
			id,
			result: {
				protocolVersion: params.protocolVersion,
				capabilities: { tools: {}, resources: {} },
				serverInfo: { name: "echo", version: "1.0.0" },
				instructions: "Echo server for tests.",
			},
		});
	} else if (method === "tools/list") {
		send({ id, result: { tools } });
	} else if (method === "tools/call") {
		const { name, arguments: args } = params;
		if (name === "add") send({ id, result: { content: [{ type: "text", text: String(args.a + args.b) }], structuredContent: { sum: args.a + args.b } } });
		else send({ id, result: { content: [{ type: "text", text: `echo: ${args.text}` }] } });
	} else if (method === "resources/list") {
		send({ id, result: { resources: [{ uri: "test://note", name: "note", mimeType: "text/plain" }] } });
	} else if (method === "resources/read") {
		send({ id, result: { contents: [{ uri: params.uri, mimeType: "text/plain", text: "resource body MCP-RES" }] } });
	} else {
		send({ id, result: {} });
	}
}
