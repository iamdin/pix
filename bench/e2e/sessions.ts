import fs from "node:fs";
import path from "node:path";
import { type Agent, agent, allRequests, expect, group, requests, run, script } from "./harness.ts";

const test = group("sessions");

const sessionRun = (a: Agent, args: string[]) => run(a, ["-p", "--session-dir", path.join(a.home, "s"), "--model", "fake/fake", ...args]);
const sessionFiles = (a: Agent) =>
	fs.existsSync(path.join(a.home, "s"))
		? fs
				.readdirSync(path.join(a.home, "s"), { recursive: true })
				.map(String)
				.filter((f) => f.endsWith(".jsonl"))
				.map((f) => path.join(a.home, "s", f))
		: [];

test("a session is saved as JSONL and -c continues it", async () => {
	const a = agent();
	script();
	await sessionRun(a, ["FIRST-MARK"]);
	script();
	const r = await sessionRun(a, ["-c", "second"]);
	const [file] = sessionFiles(a);
	const entries = file ? fs.readFileSync(file, "utf8").trim().split("\n").map((l) => JSON.parse(l)) : [];
	expect(
		r.code === 0 && allRequests().includes("FIRST-MARK") && entries[0]?.type === "session" && entries.filter((e) => e.type === "message").length >= 4,
		`${r.output} ${sessionFiles(a)} ${entries.map((e) => e.type)}`,
	);
});

test("--session-id creates and reopens a named session", async () => {
	const a = agent();
	script();
	await sessionRun(a, ["--session-id", "e2e-id", "ID-MARK"]);
	script();
	await sessionRun(a, ["--session-id", "e2e-id", "again"]);
	expect(allRequests().includes("ID-MARK") && sessionFiles(a).length === 1, `${sessionFiles(a)} ${allRequests().slice(0, 500)}`);
});

test("--session opens a file; --fork copies it into a new session", async () => {
	const a = agent();
	script();
	await sessionRun(a, ["ORIGINAL-MARK"]);
	const [file] = sessionFiles(a);
	script();
	await sessionRun(a, ["--session", file, "on original"]);
	const resumed = allRequests().includes("ORIGINAL-MARK");
	script();
	await sessionRun(a, ["--fork", file, "on fork"]);
	const forked = allRequests().includes("ORIGINAL-MARK");
	expect(resumed && forked && sessionFiles(a).length === 2, `resumed=${resumed} forked=${forked} files=${sessionFiles(a).length}`);
});

test("--name names the session", async () => {
	const a = agent();
	script();
	await sessionRun(a, ["--name", "NAME-MARK", "hi"]);
	const [file] = sessionFiles(a);
	expect(file && fs.readFileSync(file, "utf8").includes("NAME-MARK"), `${file}`);
});

test("--export writes HTML", async () => {
	const a = agent();
	script();
	await sessionRun(a, ["EXPORT-MARK"]);
	const html = path.join(a.home, "out.html");
	const r = await run(a, ["--export", sessionFiles(a)[0] ?? "missing", html]);
	expect(fs.existsSync(html) && /<html/i.test(fs.readFileSync(html, "utf8")), r.output);
});

test("--no-session writes nothing", async () => {
	const a = agent();
	script();
	await run(a, ["-p", "--no-session", "--session-dir", path.join(a.home, "s"), "--model", "fake/fake", "hi"]);
	expect(sessionFiles(a).length === 0 && requests.length === 1, `${sessionFiles(a)}`);
});
