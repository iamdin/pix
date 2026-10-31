# pitch

用 Go 编排固定 Pi 的官方 Bun AOT 构建。**当前 alpha.10，仅支持 macOS ARM64，仍为实验性工具链。**

## 安装

```sh
curl -fsSL https://raw.githubusercontent.com/iamdin/pitch/main/install.sh | sh
```

装到 `~/.local/bin/pitch`（`PITCH_INSTALL_DIR` 可改），校验 sha256。pitch 自带 Bun 和已经打好补丁、构建并测试过的 Pi，`pitch build` 只在本机编译你的扩展，约一分钟；不需要 Node。来自 npm 的扩展用 npm 下载，来自 Git 的用 git。不改动全局安装。从源码构建：`go build -trimpath -o bin/pitch ./cmd/pitch`（需要 Go 1.24+ 和本地的 `internal/pitch/runtime-data/bun.zip`，它不在仓库里。这样的开发构建不带 Pi：先 `pitch prepare /abs/pi.tar.gz`（需要 Git、Node 24+ 和 npm），再 `pitch build --pi /abs/pi.tar.gz --out DIR`；`bun scripts/release.ts` 会把 Pi 打进 pitch）。

## 使用

```sh
pitch build              # 按 ~/.pi/agent/settings.json 的扩展构建（扩展变了再跑一次）
pitch pi                 # 运行构建好的 Pi；pi 后面的参数原样交给 Pi：pitch pi -c、pitch pi install npm:x
pitch status             # 版本、补丁、编进去的扩展、设置是否有变化
pitch off / pitch on     # 换回原版 Pi / 再换回来（临时一次：PITCH_OFF=1 pitch pi）
pitch uninstall          # 删掉 ~/.pitch、它的构建和加过的 PATH 行

pitch build --name pi    # 另装一个 `pi` 命令替换原版 Pi，它就是 `pitch pi`
```

- 原版 Pi 一个文件都不改。`pitch pi install/remove/uninstall/update/config` 由原版 Pi 执行，执行完提示 `pitch build`。
- 原版 Pi 升级到比 pitch 构建更新的版本时，自动运行原版并提示一行，等 pitch 跟上后再 `pitch build`。
- 新构建通过自检才切换；保留上一个构建给还在运行的会话，更旧的连同缓存里的构建目录一起删掉。

## 构建

```sh
pitch build --out ./dist/my-pi
./dist/my-pi/pi

# 新建配置文件，格式沿用 Pi settings 的 packages
pitch build --settings ./pitch-settings.json --out ./dist/my-pi-ext
```

`pitch-settings.json` 示例：

```json
{
  "packages": [
    "npm:@getpie/pi-loop@0.0.7",
    "git:github.com/example/tools@v1.0.0",
    "./local-tools",
    { "source": "npm:another-package", "extensions": [] }
  ]
}
```

示例中的 Git 和 another-package 为占位来源，使用时替换或删除。只读取 `packages`，不复制模型、凭据或其他设置。相对本地路径从这个配置文件所在目录解析。不会默认读取用户 settings 或扫描本机已安装扩展。

也可直接给一个或多个字符串来源：

```sh
./bin/pitch build --extension npm:@getpie/pi-loop --out ./dist/pi-loop
./dist/pi-loop/pi
```

`--extension` 可重复，与 `--settings` 一起使用时追加到声明列表末尾。本地 CLI 来源相对当前工作目录解析。旧 `NAME=/entry.ts` 仍接受，但 NAME 不再生成运行时别名。输出目录必须不存在，不覆盖失败或已有产物。

## 原生声明到 AOT

1. 调用固定版本 Pi 的包管理器，按原生来源语法处理 npm、Git/URL、本地目录或文件。保留 npm 版本和 Git ref。
2. 复用 Pi 的 `packages` schema、包去重、manifest/目录发现和资源筛选，包括对象的 `extensions: []`、glob、排除和 `autoload`。不另造包身份协议。
3. 使用独立构建目录中的临时项目安装依赖，避开用户安装及 Pi 的历史全局 npm 回退；本地包不自动安装依赖或修改其目录。
4. 把 Pi 和选中的扩展入口作为 Bun 的多个编译入口，使用 `--compile --aot --compile-in-place`。没有扩展工厂聚合或自定义 builtin 名称。
5. 生成的启动入口把编译选中的路径自动传给 Pi 原有 `-e` 参数。**编译选中的扩展自动加载，不需要用户运行时再次指定。**

Go CLI 是编排主体；小型 JS 桥接直接使用这份 Pi 的包管理器，不重写它的 npm/Git 解析。将本地来源转换为配置基准目录下的绝对路径时使用固定 Pi 的内部解析器，因此升级 Pi 时必须跑解析回归测试。

## 加载和安全边界

- 与 alpha.3 不同，编译选中的扩展现在自动执行，包括模块顶层及工厂；构建中的 help/RPC smoke 也可能执行它们。**只构建可信包。隔离 HOME/目录不是权限沙箱。**
- 自动加载的仅是构建选中的扩展。启动入口仍传 `--no-extensions`，不自动执行用户、项目或设置里的其他扩展；运行时额外指定 `-e` 仍可加载磁盘扩展，但不属于本次 AOT 代码。
- 不需要 `builtin:pitch-NAME`，没有新增扩展 URI/ID 协议。扩展工具和命令名称不改写，冲突按 Pi 处理。
- 技能、提示词、主题、模型、凭据、会话仍读取正常用户配置；构建包中的 skills/prompts/themes 不自动一起分发。
- npm 生命周期脚本禁用，不读取用户/全局 npm 配置。依赖安装脚本或私有仓库凭据的包可能无法构建；Bun 宏等编译时代码仍可能执行。
- 首参数为 install/remove/uninstall/update/list/config/auth/mcp 的管理命令仍拒绝。不要通过自更新覆盖定制程序。

## 固定工具链和优化

| 组件 | 固定版本 |
|---|---|
| Pi | v1.4.0 加 pitch 补丁：[getpie-dev/pi](https://github.com/getpie-dev/pi) `pitch-v1.4.0`，d95a12ee1b88c6d216468b4eb8a5d388d0623a3e |
| Bun | 000ef503730334a388c47b1bd025851a76b4832e |
| WebKit/JSC | 8c6c963e3c49fe39942d9625d1fc924128fad26e |

不提供 Pi/Bun 版本选择。Pi 只跟上游发布 tag，pitch 的补丁是 fork 上的提交（见 [PATCHES.md](PATCHES.md)）；Bun AOT 只在 canary 中提供，因此 Bun 固定到某个官方 canary 构建，随维护者升级。Bun 固定归档和准备好的 Pi 嵌入 Go CLI（共约 100 MiB），校验归档/程序哈希、revision 和签名；不依赖已删除的上游下载资产，不静默升级。Git/npm/模型目录的冷获取仍需要网络。当前使用官方预编译 Bun，不是自建优化版 Bun/JSC。

**Pi 带 5 个经过 AOT 性能关卡的优化补丁**（见下）。

每个补丁是一种独立的优化方法，也是 fork 上的一个提交；发版时运行每个补丁自带的测试。方法、不变量和测量见 `docs/pi-perf/<name>/SPEC.md`，结果见 `docs/pi-perf/results/v1.4.0/log.md`。

| 补丁 | 方法 |
|---|---|
| `streaming-markdown` | 流式回复只重新解析、渲染变化的块 |
| `highlight-cache` | 代码块高亮结果复用，不每帧重算 |
| `streaming-tool-arguments` | 流式工具参数按几何间隔重新解析 |
| `write-call-highlight` | 折叠的 write 调用只高亮显示的行 |
| `native-image-resize` | 大图用 Bun.Image 原生缩放，不用 Photon（AOT 没有 JIT，WebAssembly 慢几十倍） |

v1.4.0 官方 AOT，原版 → 默认（Apple M4，CPU 时间 / RSS）：6 万字流式 Markdown 24.2 s → 4.8 s；200 KB write（`pi -p`）9.7 s → 0.1 s；TUI 中 200 KB write 内存 316 MB → 105 MB；启动不变；读一张 3000 px 图片 25 s → 0.3 s。

**`streaming-tool-arguments` 行为差异**：大工具参数的中间解析快照更新变少，原始 delta 和最终参数保持完整。依赖每条 delta 都有最新解析快照的扩展，用 `pitch off` 换回原版 Pi。

补丁怎么维护、怎么改见 [PATCHES.md](PATCHES.md)。

## 产物与可移植性

保留完整输出目录，不只复制 `pi`。`pitch-build.json` 记录源码、工具链、入口、实际包版本/commit、参数和文件哈希，`source_tree` 记录所构建 Pi 的 Git tree，`checks` 列出实际跑过的检查，`extensions_auto_load` 标明新加载语义。`recipe/extensions.json` 保留原生 packages 声明、解析出的入口及发现的 npm 锁文件内容；不包含其他输入设置。

`--compile-in-place` 保留构建时模块身份。已验证选中的 pi-loop/受控扩展在构建目录、本地入口移走后仍自动加载；另用禁止磁盘代码和字符串代码生成的诊断构建验证不是源码回退。**这不等于任意扩展跨机器可移植。** 动态路径、原生库及运行时资源仍需逐项处理；Bun 将 in-place 定位为本机构建。

启动入口在未设置时将 `PI_PACKAGE_DIR` 指向发行目录，并分发 Pi 所需的 unbundled 资源布局，以避免版本/主题等依赖构建树。未证明完整图片、导出、所有内置扩展或任意自带资源扩展的兼容性。

未指定版本/ref 的下一次构建可能得到不同输入。当前记录依赖锁，但没有锁文件重放命令、完整输入快照或逐字节可复现保证。官方 AOT 的内置对象、动态加载与普通 Pi 有差异；运行时代码可能解释执行。

`build` 结束前会按 manifest 检查产物文件哈希；这是构建自检，不是发行签名。当前没有正式发布渠道、Apple 公证或完整再分发合规认证；保留许可证，公开分发需遵守相应第三方条款。

## 验证

```sh
go test -race ./...
go vet ./...

# 实际产物：默认加载、磁盘扩展、管理命令和本地 SSE 写入等测试
PITCH_TEST_ARTIFACT="$PWD/dist/my-pi" go test -race ./...

# 固定 Pi 工作树必须已完成 offline 构建；包测试使用本地 npm/Git fixtures
PITCH_TEST_PI=/absolute/build-work/pi go test -race ./...

# Pi 原生能力端到端（CLI、工具、codemode、MCP、会话、技能/模板/扩展、RPC、TUI），用本地假模型
# 先在原版 Pi 上跑通，再跑构建产物
bun bench/e2e/run.ts ~/.bun/bin/pi
bun bench/e2e/run.ts "$PWD/dist/my-pi/pi"
bun bench/e2e/run.ts bin/pitch pi        # 经 pitch pi 跑已构建的版本
```

未设置环境变量时，相应集成测试会跳过。性能记录见 `docs/pi-perf/results/`。失败构建和实验保留在 `.work/` 或系统 pitch 缓存，不修改历史 pix 产物。

## 致谢

感谢 Pi、Bun 和 JavaScriptCore 的开发者；感谢 Pi-Bolt 提供优化研究参考。前 4 个优化补丁参考 Pi-Bolt 的对应改动实现（commit 见各 SPEC），保留 MIT 许可证，启用任一补丁时产物附带 `LICENSE.Pi-Bolt`。
