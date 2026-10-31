// An extension loaded from disk at run time (-e): a tool, a command, a flag, an event hook and RPC dialogs.
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { Type } from "typebox";

export default function (pi: ExtensionAPI) {
	pi.registerFlag("e2e-flag", { description: "test flag", type: "string" });

	pi.registerTool({
		name: "e2e_tool",
		label: "E2E tool",
		description: "Returns a fixed marker and the text it got.",
		parameters: Type.Object({ text: Type.String() }),
		async execute(_id, params) {
			return { content: [{ type: "text", text: `EXT-TOOL:${params.text}:${pi.getFlag("e2e-flag") ?? ""}` }], details: {} };
		},
	});

	pi.registerCommand("e2e-ask", {
		description: "Ask for a value and send it as a user message",
		handler: async (_args, ctx) => {
			const value = await ctx.ui.input("E2E value", "type");
			const choice = await ctx.ui.select("E2E pick", ["alpha", "beta"]);
			const ok = await ctx.ui.confirm("E2E confirm", "sure?");
			ctx.ui.notify(`E2E-NOTIFY ${value} ${choice} ${ok}`, "info");
			pi.sendUserMessage(`EXT-ASKED ${value} ${choice} ${ok}`);
		},
	});

	pi.on("before_agent_start", (event) => ({ systemPrompt: `${event.systemPrompt}\nEXT-SYSTEM-MARK` }));
}
