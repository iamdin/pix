import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, mkdirSync, readFileSync, realpathSync, statSync, symlinkSync, writeFileSync } from "node:fs";
import { basename, dirname, join, sep } from "node:path";
import { pathToFileURL } from "node:url";

const request = JSON.parse(readFileSync(process.argv[2], "utf8"));
const core = join(request.pi, "packages/coding-agent/dist/core");
const { DefaultPackageManager } = await import(pathToFileURL(join(core, "package-manager.js")));
const { SettingsManager } = await import(pathToFileURL(join(core, "settings-manager.js")));
const { SettingsSchema } = await import(pathToFileURL(join(core, "settings-schema.js")));
const { Check } = await import(pathToFileURL(join(request.pi, "node_modules/typebox/build/value/index.mjs")));
const { resolvePath } = await import(pathToFileURL(join(core, "../utils/paths.js")));
const sha256 = (data) => createHash("sha256").update(data).digest("hex");
const readJSON = (file) => JSON.parse(readFileSync(file, "utf8").replace(/^\uFEFF/, ""));
const packages = [];
if (request.settings) {
  const settings = readJSON(request.settings);
  if (!settings || typeof settings !== "object" || Array.isArray(settings) || !Array.isArray(settings.packages)) {
    throw new Error("Settings input must contain a packages array");
  }
  for (const declaration of settings.packages) packages.push({ declaration, baseDir: dirname(request.settings) });
}
for (const value of request.values ?? []) {
  // Preserve the old NAME=/file spelling; aliases no longer change runtime identity.
  const alias = /^([a-z][a-z0-9-]{0,63})=(.+)$/s.exec(value);
  packages.push({ declaration: alias ? alias[2] : value, baseDir: request.cwd });
}
if (!Check(SettingsSchema, { packages: packages.map((item) => item.declaration) })) {
  throw new Error("Invalid Pi packages declaration (expected string or source/resource-filter object)");
}
const result = { packages: packages.map((item) => item.declaration), extensions: [], locks: [], assets: [] };
const workspace = join(request.stage, "extension-packages");
mkdirSync(workspace, { recursive: true, mode: 0o700 });
const settingsManager = SettingsManager.inMemory({
  npmCommand: ["npm", "--ignore-scripts", "--no-audit", "--no-fund", "--package-lock=true"],
});
settingsManager.setProjectTrusted(true);
const manager = new DefaultPackageManager({ cwd: workspace, agentDir: join(workspace, "agent"), settingsManager });
const originalSources = new Map();
const declarations = packages.map(({ declaration, baseDir }) => {
  const source = typeof declaration === "string" ? declaration : declaration.source;
  if (!source.trim()) throw new Error("Empty extension source");
  if (source.startsWith("builtin:")) throw new Error("Pi built-ins are not package sources");
  const expanded = source === "~" ? request.userHome : source.startsWith("~/") ? join(request.userHome, source.slice(2)) : source;
  // Pinned Pi internal parser: do not duplicate npm/git/local syntax in pix.
  const parsed = manager.parseSource(expanded);
  const resolved = parsed.type === "local" ? resolvePath(parsed.path, baseDir) : expanded;
  if (parsed.type === "local" && !existsSync(resolved)) throw new Error("Package path does not exist: " + resolved);
  originalSources.set(resolved, source);
  return typeof declaration === "string" ? resolved : { ...declaration, source: resolved };
});
settingsManager.setProjectPackages(declarations);
await settingsManager.flush();
process.env.PI_OFFLINE = "0";
process.env.npm_config_userconfig = join(request.stage, "home", ".npmrc");
process.env.npm_config_globalconfig = join(request.stage, "home", "global.npmrc");
console.error("Resolve Pi packages in isolated project storage (install scripts disabled)");
// AOT type-checks Pi and the extensions together: a second @types/node (e.g. a newer one pulled by @types/ws) breaks
// Pi's own types. Pin the npm packages to Pi's copy; Pi keeps an existing package.json.
const npmRoot = join(workspace, ".pi", "npm");
mkdirSync(npmRoot, { recursive: true });
const typesNode = readJSON(join(request.pi, "node_modules/@types/node/package.json")).version;
writeFileSync(join(npmRoot, "package.json"), JSON.stringify({ name: "pi-extensions", private: true, overrides: { "@types/node": typesNode } }, null, 2));
// Project scope avoids Pi's legacy global-npm fallback. Only the in-memory packages list is used.
const resources = await manager.resolve();
const selected = [];
for (const [index, resource] of resources.extensions.filter((item) => item.enabled && item.metadata.origin === "package").entries()) {
  if (!statSync(resource.path).isDirectory()) { selected.push(resource); continue; }
  const discovery = join(workspace, "discovery", String(index));
  mkdirSync(discovery, { recursive: true });
  symlinkSync(resource.path, join(discovery, "extensions"), "dir");
  const nested = await manager.resolveExtensionSources([discovery], { temporary: true });
  selected.push(...nested.extensions.filter((item) => item.enabled).map((item) => ({ ...item, metadata: resource.metadata })));
}
const entries = new Set();
for (const resource of selected) {
  let entry = realpathSync(resource.path);
  // A manifest entry may name a directory, even its own ("./"): Pi imports it, which loads its index.
  if (statSync(entry).isDirectory()) entry = ["index.ts", "index.js"].map((name) => join(entry, name)).find((file) => existsSync(file)) ?? entry;
  if (entries.has(entry)) continue;
  entries.add(entry);
  const root = resource.metadata.packageRoot;
  const packageJSON = root && join(root, "package.json");
  const pkg = packageJSON && existsSync(packageJSON) ? readJSON(packageJSON) : {};
  if (packageJSON && existsSync(packageJSON) && !result.assets.includes(packageJSON)) result.assets.push(packageJSON);
  const record = { name: pkg.name ?? basename(entry), entry, source: originalSources.get(resource.metadata.source) ?? resource.metadata.source, package: pkg.name, version: pkg.version };
  if (root && existsSync(join(root, ".git"))) {
    record.commit = execFileSync("git", ["rev-parse", "--verify", "HEAD"], { cwd: root, encoding: "utf8" }).trim();
  }
  if (root) {
    const managed = realpathSync(root).startsWith(realpathSync(workspace) + sep);
    let current = root;
    while (true) {
      const lock = join(current, "package-lock.json");
      if (existsSync(lock)) {
        const content = readFileSync(lock, "utf8");
        record.dependency_lock_sha256 = sha256(content);
        if (!result.locks.some((item) => item.path === lock)) result.locks.push({ path: lock, content });
        break;
      }
      const parent = dirname(current);
      if (!managed || parent === current || !parent.startsWith(workspace + sep)) break;
      current = parent;
    }
  }
  result.extensions.push(record);
}
writeFileSync(request.output, JSON.stringify(result, null, 2) + "\n", { flag: "wx", mode: 0o600 });
