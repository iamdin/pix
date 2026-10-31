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

## 来源记录

`pitch-build.json` 的 `sources.pi` 记录构建的 Pi：fork、分支和 commit；`source_tree` 是该 commit 的 Git tree，`checks` 列出实际跑过的检查（补丁单测只在准备锁定的 commit 时跑）。不承诺生成的二进制逐字节一致。

## 改补丁

在 fork 里改提交，再用 `pitch prepare --repo /abs/pi --commit SHA /abs/pi.tar.gz` 构建出 Pi（需要 Git、Node 24+ 和 npm），`pitch build --pi /abs/pi.tar.gz --out NEW` 编译。比较补丁组合用 `bench/build-variants.ts`，它在 fork 里 revert 掉不要的补丁再走同样两步。
