# Patch 使用与贡献

pitch alpha.8 仍固定 Pi/Bun 版本；通用的是 patch 输入与构建流程，不是任意上游/平台构建器。

## 使用自己的补丁

1. 基于 `pitch features --json` 中的 Pi commit 准备源码。
2. 当前默认 `--features all`：先应用内置补丁，自己的补丁基于该组合制作；基于原版制作时传 `--features none`。
3. 使用标准 Git diff，带 `a/`、`b/` 路径。包含新增文件时要让 Git 识别这些文件，例如对自己新增的文件执行 `git add -N path`。
4. `pitch build --patch first.patch --patch second.patch --out NEW`；顺序就是应用顺序。带空格的路径使用 shell 引号。
5. 检查 manifest 和自己的功能测试。build 的哈希自检只校验本地文件完整性，不认证发布者、业务正确性或隔离安全性。

内置补丁可任意子集、任意顺序应用（`bench/check-patch-orders.ts` 检查），选择按 catalog 顺序规范化，外部补丁按命令行顺序接在后面。未知/重复 feature 拒绝；重复外部补丁按正常应用规则失败。每一步使用 Git 的索引检查，不使用三方合并、忽略上下文或自动修复。Git 的标准上下文匹配仍可能容许行号偏移；这不是逐行行号锁定。失配时保留工作树和日志，由维护者/Agent 修正补丁后使用新输出目录重试。

本地 patch 在准备前读入并固定副本。拒绝空文件、目录、符号链接和超过 16 MiB 的文件。Git 拒绝目录穿越及穿越工作树符号链接写入，但这**不是执行沙箱**；可信源码才可编译、测试和运行。

## 来源记录与重放

`pitch-build.json` 的 `patches` 数组按应用顺序记录：

- `kind`：`builtin` / `local`；
- `source`：内置 ID / 本地输入的绝对路径（分享 manifest 前注意路径隐私）；
- `file`：产物中的 recipe 副本；
- `sha256`：该副本的字节哈希。

默认组合变更后，不能仅靠 `build` 重放旧组合。保留旧 CLI、manifest 和 recipe。使用同一固定 Pi/Bun 的 CLI，传 `--features none`，再按 manifest 顺序把**所有** recipe patch 逐个传给 `--patch`，可重建相同源码树；比较 `source_tree`。这样会把历史内置 patch 作为本地 patch，需另行运行对应测试并保留许可证。不承诺生成的二进制逐字节一致，也没有自动 manifest 重放命令。

## 内置补丁

内置补丁来自 `docs/pi-perf/<name>/`：`SPEC.md` 是权威说明，`<tag>.diff` 是在该 Pi tag 上的实现。一个补丁 = 一种优化方法，不编号，效果上互不依赖。新增、升级和性能关卡按 `.agents/skills/pi-perf-upgrade/SKILL.md` 执行：单独打在 tag 上任何场景不退化超过 5%，从全套中去掉它能看出收益。

通过后：

- 把 `docs/pi-perf/<name>/<tag>.diff` 复制为 `internal/pitch/data/<name>.patch`，在 `catalog.go` 更新 SHA-256、说明和测试命令；
- `PITCH_TEST_PI=<pinned pristine Pi> go test ./...` 检查所有子集都能应用；
- 用默认参数构建一次，确认每个补丁的测试和 smoke 通过。

不提供 patch 市场、远程自动安装或签名投稿系统。
