// A minimal MCP stdio server with one tool, `echo`, for bench/pi-capabilities.ts.
import readline from "node:readline";

const send = (message) => process.stdout.write(`${JSON.stringify({ jsonrpc: "2.0", ...message })}\n`);
for await (const line of readline.createInterface({ input: process.stdin })) {
	const { id, method, params } = JSON.parse(line);
	if (id === undefined) continue;
	if (method === "initialize") {
		send({ id, result: { protocolVersion: params.protocolVersion, capabilities: { tools: {} }, serverInfo: { name: "echo", version: "1.0.0" } } });
	} else if (method === "tools/list") {
		send({ id, result: { tools: [{ name: "echo", description: "Echo the text.", inputSchema: { type: "object", properties: { text: { type: "string" } }, required: ["text"] } }] } });
	} else if (method === "tools/call") {
		send({ id, result: { content: [{ type: "text", text: `echo: ${params.arguments.text}` }] } });
	} else {
		send({ id, result: {} });
	}
}
