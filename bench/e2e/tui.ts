import fs from "node:fs";
import path from "node:path";
import { type Agent, agent, allRequests, expect, fixtures, group, requests, script, Tty } from "./harness.ts";

const test = group("tui");

async function withTty(fn: (tty: Tty, a: Agent) => Promise<void>, args: string[] = []) {
	const a = agent();
	const tty = new Tty(a, ["--session-dir", path.join(a.home, "s"), "--model", "fake/fake", ...args]);
	try {
		expect(await tty.waitFor("fake"), `did not start:\n${tty.text.slice(-2000)}`);
		await tty.settle();
		await fn(tty, a);
	} finally {
		await tty.close();
	}
}

/** Submits a line and waits for `needle` in the output that follows. */
async function submit(tty: Tty, line: string, needle: string | RegExp, timeoutMs = 20_000) {
	const mark = tty.mark;
	await tty.submit(line);
	expect(await tty.waitFor(needle, mark, timeoutMs), `"${line}" did not show ${needle}:\n${tty.text.slice(mark).slice(-2000)}`);
	await tty.settle();
}

test("a prompt with a tool call renders the answer", async () => {
	script({ name: "bash", args: { command: "echo TTY-BASH" } }, { text: "TTY-ANSWER" });
	await withTty(async (tty) => {
		await submit(tty, "go", "TTY-ANSWER");
		expect(tty.text.includes("TTY-BASH"), tty.text.slice(-2000));
	});
});

test("!command runs bash; !!command keeps it out of context", async () => {
	script();
	await withTty(async (tty) => {
		await submit(tty, "!echo BANG-$((6*7))", "BANG-42");
		await submit(tty, "!!echo HIDDEN-$((1+1))", "HIDDEN-2");
		await submit(tty, "next", "Done.");
		expect(allRequests().includes("BANG-42") && !allRequests().includes("HIDDEN-2"), allRequests().slice(0, 2000));
	});
});

test("slash commands: /name, /session, /hotkeys, /changelog", async () => {
	script();
	await withTty(async (tty) => {
		await submit(tty, "hi", "Done.");
		await submit(tty, "/name TTY-NAME", "TTY-NAME");
		await submit(tty, "/session", /\.jsonl/);
		await submit(tty, "/hotkeys", /ctrl|Ctrl/);
		await submit(tty, "/changelog", /Fixed|Added|Changed/);
	});
});

test("/model switches the model", async () => {
	script();
	await withTty(async (tty) => {
		const mark = tty.mark;
		// The first Enter takes the autocompletion (fake/fake2), the second runs the command.
		await tty.submit("/model fake2");
		await tty.settle();
		tty.send("\r");
		expect(await tty.waitFor("Model: fake2", mark), tty.text.slice(mark).slice(-1000));
		await submit(tty, "hi", "Done.");
		expect(requests.at(-1)?.body.model === "fake2", `${requests.at(-1)?.body.model}\n${tty.text.slice(mark).slice(-2000)}`);
	});
});

test("/compact, /export, /new", async () => {
	script();
	await withTty(async (tty, a) => {
		await submit(tty, "one", "Done.");
		await submit(tty, "two", "Done.");
		await submit(tty, "/compact", /[Cc]ompact/, 30_000);
		const html = path.join(a.home, "tty.html");
		await submit(tty, `/export ${html}`, "tty.html");
		await submit(tty, "/new", /[Nn]ew session|session/);
		expect(fs.existsSync(html), "no export");
	});
});

test("/copy puts the last answer on the clipboard", async () => {
	script({ text: `COPY-${Date.now()}` });
	await withTty(async (tty) => {
		await submit(tty, "hi", "COPY-");
		const want = tty.text.match(/COPY-\d+/)?.[0];
		await submit(tty, "/copy", /[Cc]opied/);
		const clip = Bun.spawnSync(["pbpaste"]).stdout.toString();
		expect(clip.includes(want ?? "?"), `clipboard: ${clip.slice(0, 100)}`);
	});
});

test("escape aborts a streaming answer", async () => {
	script();
	await withTty(async (tty) => {
		const mark = tty.mark;
		await tty.submit("SLOW please");
		expect(await tty.waitFor("word3", mark), tty.text.slice(mark).slice(-1000));
		tty.send("\x1b");
		expect(await tty.waitFor(/[Aa]bort/, mark, 5000), tty.text.slice(mark).slice(-1000));
	});
});

test("--use-theme loads a theme file", async () => {
	script();
	await withTty(
		async (tty) => {
			await submit(tty, "hi", "Done.");
		},
		["--theme", path.join(fixtures, "themes/e2e-theme.json"), "--use-theme", "e2e-theme"],
	);
});

test("/quit exits", async () => {
	await withTty(async (tty) => {
		await tty.submit("/quit");
		await Promise.race([tty.proc.exited, Bun.sleep(5000)]);
		expect(tty.proc.exitCode === 0, `exit ${tty.proc.exitCode}`);
	});
});
