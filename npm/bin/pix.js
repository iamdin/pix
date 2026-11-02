#!/usr/bin/env node
// Runs pix, installed on first use by pix's install.sh into $PIX_HOME/bin (~/.pix/bin); `pix update` updates it there.
const childProcess = require("node:child_process");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");

const dir = path.join(process.env.PIX_HOME || path.join(os.homedir(), ".pix"), "bin");
const pix = path.join(dir, "pix");
if (!fs.existsSync(pix)) {
	const install = "curl -fsSL https://raw.githubusercontent.com/iamdin/pix/main/install.sh | sh";
	// With dir on PATH, install.sh does not ask to add it.
	const env = { ...process.env, PIX_INSTALL_DIR: dir, PATH: `${dir}:${process.env.PATH}` };
	const result = childProcess.spawnSync("sh", ["-c", install], { stdio: ["ignore", "inherit", "inherit"], env });
	if (result.status !== 0) process.exit(result.status ?? 1);
}
const child = childProcess.spawn(pix, process.argv.slice(2), { stdio: "inherit" });
// The terminal sends Ctrl+C to pix too; other signals are passed on.
process.on("SIGINT", () => {});
for (const signal of ["SIGTERM", "SIGHUP"]) process.on(signal, () => child.kill(signal));
child.on("exit", (code, signal) => {
	if (signal) {
		process.removeAllListeners(signal);
		process.kill(process.pid, signal);
	}
	process.exit(code ?? 1);
});
