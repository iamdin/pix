/**
 * End-to-end tests of Pi's own capabilities against a Pi binary, with a scripted fake model.
 *
 *   bun bench/e2e/run.ts <pi> [--only <substring>]
 *
 * Run it on stock Pi first: a test that fails there is a wrong test, not a pitch bug.
 */
import { Failure, server, tests } from "./harness.ts";
import "./cli.ts";
import "./tools.ts";
import "./codemode.ts";
import "./mcp.ts";
import "./sessions.ts";
import "./resources.ts";
import "./rpc.ts";
import "./tui.ts";

const only = process.argv.includes("--only") ? process.argv[process.argv.indexOf("--only") + 1] : "";
let failed = 0;
for (const t of tests) {
	const name = `${t.group}: ${t.name}`;
	if (only && !name.includes(only)) continue;
	const start = performance.now();
	try {
		await t.fn();
		console.log(`ok    ${name} (${Math.round(performance.now() - start)} ms)`);
	} catch (error) {
		failed++;
		const detail = error instanceof Failure ? error.message : String((error as Error)?.stack ?? error);
		console.log(`FAIL  ${name}\n      ${detail.slice(0, 3000).replaceAll("\n", "\n      ")}`);
	}
}
console.log(failed ? `\n${failed} failed` : "\nall passed");
server.stop(true);
process.exit(failed ? 1 : 0);
