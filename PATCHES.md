# 补丁

## pitch 的补丁

pitch 构建的 Pi 是它的 fork，仓库里不维护补丁文件。Fork 规范：

- 仓库：[getpie-dev/pi](https://github.com/getpie-dev/pi)，fork 自 earendil-works/pi。补丁只在这里维护，不提给上游。
- 分支：每个上游发布 tag 一个，命名 `pitch-<tag>`，例如 `pitch-v1.4.0`。内容是上游 tag 加上每个补丁一个提交 `perf(<name>): <summary>`，与 `docs/pi-perf/<name>/` 一一对应。
- 固定：`internal/pitch/data/sources.lock.json` 按 commit 固定，`branch` 必须是 `pitch-` 加 `tag`（`TestPins` 检查）。
- 只增不改：分支一旦被 pitch 发版固定，就不删除、不 force push，否则旧版 pitch 固定的 commit 会拉不到。发版后要修补丁，在分支上追加 `fix(<name>): ...` 提交；下次 Pi 升级建新分支时再合进对应的 `perf` 提交。
- 推送：用 gh 账号 oxwen11（getpie-dev 的 admin）。

每个补丁是一种优化方法，说明在 `docs/pi-perf/<name>/SPEC.md`：方法、不变量、测试、性能关卡、何时放弃。SPEC 是权威，提交是它在某个 tag 上的实现。Pi 发新版时从上一个分支 rebase 出新分支 `pitch-<新 tag>`，按 SPEC 逐个检查、跑测试和性能关卡，见 `.agents/skills/pi-perf-upgrade/SKILL.md`。

发版（`bun scripts/release.ts`）时在维护者机器上准备一次：拉固定 commit、`npm ci`、构建、跑全部补丁测试，打包嵌入 pitch。用户的 `pitch build` 只编译。

## 使用自己的补丁

1. 基于 `getpie-dev/pi` 的 `pitch-<tag>` 分支（`sources.lock.json` 里的 commit）制作补丁。
2. 使用标准 Git diff，带 `a/`、`b/` 路径。包含新增文件时要让 Git 识别这些文件，例如对自己新增的文件执行 `git add -N path`。
3. `pitch build --patch first.patch --patch second.patch --out NEW`；顺序就是应用顺序。带空格的路径使用 shell 引号。
4. 这时 Pi 在本机准备，需要 Git、Node 24+ 和 npm；pitch 的补丁测试不再运行（你的补丁可能改到它们），自己的测试自行运行。

每一步使用 Git 的索引检查，不使用三方合并、忽略上下文或自动修复。失配时保留工作树和日志，修正补丁后用新输出目录重试。

本地 patch 在准备前读入并固定副本。拒绝空文件、目录、符号链接和超过 16 MiB 的文件。Git 拒绝目录穿越及穿越工作树符号链接写入，但这**不是执行沙箱**；可信源码才可编译、测试和运行。

## 来源记录

`pitch-build.json` 的 `sources.pi` 记录 fork、分支和 commit；`patches` 数组按应用顺序记录本地补丁：

- `kind`：`local`；
- `source`：本地输入的绝对路径（分享 manifest 前注意路径隐私）；
- `file`：产物中的 recipe 副本；
- `sha256`：该副本的字节哈希。

`source_tree` 是打完所有补丁后的 Git tree，可用同一 commit 加同样的补丁核对。不承诺生成的二进制逐字节一致。
