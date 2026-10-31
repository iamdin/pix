/**
 * A local OpenAI-compatible model with deterministic answers. The last user message picks the scenario:
 *   SCENARIO md:N      a Markdown answer of N characters, 24 characters per chunk, `paceMs` apart
 *   SCENARIO write:KB  a `write` tool call whose content is KB KiB, 16 characters per chunk; then "Done: write."
 * Anything else gets "Done.".
 */

const MARKDOWN = `## Section {n}

Some **bold** text, some \`inline code\` and a [link](https://example.com/{n}). A longer sentence follows so that the
paragraph wraps on a normal terminal width and the renderer has real work to do for each line of it.

- first item of list {n}
- second item with *emphasis*
  - a nested item
1. an ordered item
2. another one

\`\`\`ts
export function section{n}(value: number): string {
\treturn \`value \${value * {n}}\`;
}
\`\`\`

| name | value | note |
|------|------:|------|
| a{n} | {n} | plain |
| b{n} | {n}0 | **bold** |

> A quote for section {n}.

`;

const WRITE_LINE = "export const value = 'abcdefghijklmnopqrstuvwxyz0123456789'; // payload line\n";

export function markdown(size: number): string {
	let body = "";
	for (let n = 1; body.length < size; n++) body += MARKDOWN.replaceAll("{n}", String(n));
	return body.slice(0, size);
}

export function writeContent(kb: number): string {
	const size = kb * 1024;
	return WRITE_LINE.repeat(Math.ceil(size / WRITE_LINE.length)).slice(0, size);
}

type Message = { role: string; content?: string | { type: string; text?: string }[] };

function chunk(model: string, delta: object, finish: string | null = null): string {
	const data = { id: "fake", object: "chat.completion.chunk", created: 0, model, choices: [{ index: 0, delta, finish_reason: finish }] };
	return `data: ${JSON.stringify(data)}\n\n`;
}

async function* answer(messages: Message[], model: string, paceMs: number): AsyncGenerator<string> {
	const user = messages.filter((m) => m.role === "user").at(-1);
	const prompt =
		typeof user?.content === "string" ? user.content : (user?.content ?? []).map((p) => p.text ?? "").join(" ");
	const toolDone = messages.at(-1)?.role === "tool";
	const match = /SCENARIO (md|write):(\d+)/.exec(prompt);

	if (match?.[1] === "md" && !toolDone) {
		const body = markdown(Number(match[2]));
		yield chunk(model, { role: "assistant", content: "" });
		for (let i = 0; i < body.length; i += 24) {
			yield chunk(model, { content: body.slice(i, i + 24) });
			if (paceMs) await Bun.sleep(paceMs);
		}
		yield chunk(model, { content: "\n\nDone: md." });
	} else if (match?.[1] === "write" && !toolDone) {
		const args = JSON.stringify({ path: "out.ts", content: writeContent(Number(match[2])) });
		yield chunk(model, {
			role: "assistant",
			tool_calls: [{ index: 0, id: "call_1", type: "function", function: { name: "write", arguments: "" } }],
		});
		for (let i = 0; i < args.length; i += 16) {
			yield chunk(model, { tool_calls: [{ index: 0, function: { arguments: args.slice(i, i + 16) } }] });
		}
		yield chunk(model, {}, "tool_calls");
		yield "data: [DONE]\n\n";
		return;
	} else {
		yield chunk(model, { role: "assistant", content: toolDone ? "Done: write." : "Done." });
	}
	yield chunk(model, {}, "stop");
	yield "data: [DONE]\n\n";
}

export function startFakeModel(paceMs: number) {
	return Bun.serve({
		port: 0,
		hostname: "127.0.0.1",
		idleTimeout: 0,
		async fetch(req) {
			if (req.method !== "POST") return Response.json({ data: [{ id: "fake-model", object: "model" }] });
			const body = (await req.json()) as { messages?: Message[]; model?: string };
			const encoder = new TextEncoder();
			const events = answer(body.messages ?? [], body.model ?? "fake-model", paceMs);
			const stream = new ReadableStream({
				async pull(controller) {
					const next = await events.next();
					if (next.done) controller.close();
					else controller.enqueue(encoder.encode(next.value));
				},
			});
			return new Response(stream, { headers: { "Content-Type": "text/event-stream", "Cache-Control": "no-cache" } });
		},
	});
}
