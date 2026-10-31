import fs from "node:fs";
import path from "node:path";
import { agent, expect, group, lastTurn, png, print, script } from "./harness.ts";

const test = group("tools");

test("read with offset and limit", async () => {
	const a = agent();
	script({ name: "read", args: { path: "hello.txt", offset: 2, limit: 1 } });
	await print(a, ["go"]);
	const t = lastTurn();
	expect(t.includes("second line") && !t.includes("hello pitch") && !t.includes("third line\\n"), t);
});

test("read of a missing file reports an error", async () => {
	const a = agent();
	script({ name: "read", args: { path: "nope.txt" } });
	await print(a, ["go"]);
	expect(/ENOENT|not found|does not exist/i.test(lastTurn()), lastTurn());
});

test("read resizes a large image", async () => {
	const a = agent();
	png(path.join(a.cwd, "big.png"));
	script({ name: "read", args: { path: "big.png" } });
	await print(a, ["go"]);
	expect(lastTurn().includes("image_url") && /resiz|3000/i.test(lastTurn()), lastTurn().slice(0, 1500));
});

test("write then edit with several replacements", async () => {
	const a = agent();
	script(
		{ name: "write", args: { path: "sub/w.txt", content: "one\ntwo\n" } },
		{ name: "edit", args: { path: "sub/w.txt", edits: [{ oldText: "one", newText: "uno" }, { oldText: "two", newText: "dos" }] } },
	);
	await print(a, ["go"]);
	const file = fs.readFileSync(path.join(a.cwd, "sub/w.txt"), "utf8");
	expect(file === "uno\ndos\n", file);
});

test("edit with no match fails", async () => {
	const a = agent();
	script({ name: "edit", args: { path: "hello.txt", edits: [{ oldText: "absent", newText: "x" }] } });
	await print(a, ["go"]);
	expect(/not find|not found|no match/i.test(lastTurn()), lastTurn());
});

test("bash output and exit code", async () => {
	const a = agent();
	script({ name: "bash", args: { command: "echo ok-$((1+1)); exit 3" } });
	await print(a, ["go"]);
	expect(lastTurn().includes("ok-2") && lastTurn().includes("3"), lastTurn());
});

test("grep, find and ls", async () => {
	const a = agent();
	script({
		calls: [
			{ name: "grep", args: { pattern: "second", path: "." } },
			{ name: "find", args: { pattern: "*.txt" } },
			{ name: "ls", args: { path: "." } },
		],
	});
	await print(a, ["--tools", "grep,find,ls", "go"]);
	const t = lastTurn();
	expect(t.includes("hello.txt:2") && (t.match(/hello\.txt/g)?.length ?? 0) >= 3, t);
});

test("parallel tool calls in one turn", async () => {
	const a = agent();
	script({
		calls: [
			{ name: "bash", args: { command: "echo P1" } },
			{ name: "bash", args: { command: "echo P2" } },
		],
	});
	await print(a, ["go"]);
	expect(lastTurn().includes("P1") && lastTurn().includes("P2"), lastTurn());
});
