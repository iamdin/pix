import { appendFileSync } from "node:fs";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { Text } from "@earendil-works/pi-tui";

const marker = process.env.PITCH_TEST_MARKER;
if (marker) appendFileSync(marker, "module\n");
export default function (pi: ExtensionAPI) {
 if (new Text("fixture", 0, 0).render(80).length !== 1) throw new Error("shared TUI import failed");
 if (marker) appendFileSync(marker, "factory\n");
 pi.registerCommand("pitch-fixture-test", { description: "Integration fixture", handler: async () => {} });
}
