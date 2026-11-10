# fnOS Codex 工作台

Codex 工作台是一款面向飞牛 fnOS 的第三方应用。管理员从飞牛桌面打开网页，选择 NAS 上的项目目录，即可与 Codex 对话、查看命令输出和文件变更，并在页面上处理审批。Vue 3 前端嵌入 Go 服务，后端通过标准输入输出调用所选版本的 `codex app-server`。同一应用还提供版本管理和可选的 Web 终端。[Codex App Server 协议](https://developers.openai.com/codex/app-server/)

当前工作台版本为 **1.3.0**，最低系统版本为 **fnOS 1.2.0401**。FPK 的应用标识沿用 `CodexTerminal`。工作台随 FPK 升级，Codex CLI 的版本由用户在应用内单独选择。

## 开发与构建

在 Linux 或 WSL 的 Linux 文件系统中准备 Go 1.27、Node.js 24、npm、Bash、Make 和飞牛官方 `fnpack`。在仓库根目录运行以下命令即可生成两个架构的安装包。如果 `fnpack` 已在 PATH 中，可省略第一行。[fnpack 工具说明](https://developer.fnnas.com/docs/cli/fnpack)

```bash
export FNPACK_BIN="/你的工具目录/fnpack"
make fpk
```

构建脚本先运行 `npm ci` 并编译前端，把静态资源暂存到 `backend/embed/`，再通过 `go:embed` 生成 Linux 可执行文件。打包脚本核对 ELF 架构，将程序、桌面入口、图标和生命周期脚本组装为 FPK。

| NAS 架构 | 控制台二进制 | 1.3.0 安装包 |
| --- | --- | --- |
| Intel / AMD（x86_64） | `dist/codex-console-amd64` | `dist/CodexTerminal-1.3.0-amd64.fpk` |
| ARM64（aarch64） | `dist/codex-console-arm64` | `dist/CodexTerminal-1.3.0-arm64.fpk` |

按开发需要可单独执行下列命令：

```bash
make                     # 构建当前平台的 backend/codex-console
bash build.sh cross      # 交叉编译 Linux AMD64、ARM64 二进制
sh package-fpk.sh        # 使用 dist/ 中已有的同版本二进制生成 FPK
make test                # 准备 embed 占位文件并运行后端测试
(cd frontend && npm ci && npm run check)  # 类型检查、lint、前端测试
```

`make noterminal` 构建当前平台的无终端版本。`bash build-noterminal.sh` 构建两个 Linux 架构的无终端二进制。两者同时关闭前端 xterm 和后端 PTY 路由，图形工作台仍可用。

1.2.0 已在 Linux 虚拟机中验证 AMD64 和 ARM64 启停。AMD64 还验证了离线扫描、按路径切换，以及普通应用账户对隔离 npm 全局前缀的卸载。飞牛应用中心的安装、目录授权和卸载向导仍待真机验收。复测同步卸载时，请使用测试前缀，同时放入 Codex 和一个无关包，检查清理后无关包仍可使用。

### 源码入口

| 要修改的功能 | 对应位置 |
| --- | --- |
| 图形聊天、审批、版本管理和终端页面 | [Vue 前端](frontend/src/views/) |
| 用户身份、管理 API 与工作区授权 | [HTTP 入口](backend/console.go)、[工作区校验](backend/workspace.go) |
| Codex 协议、会话状态与审批 | [App Server 客户端](backend/internal/codex/)、[工作台服务](backend/internal/workbench/) |
| CLI 安装、本机版本扫描和卸载清理 | [安装器](backend/codex_install.go)、[本机扫描](backend/codex_discovery.go)、[Linux 卸载逻辑](backend/codex_uninstall_linux.go) |
| 飞牛应用元数据、依赖、权限、桌面入口和生命周期 | [FPK 模板](fnpack/)、[打包脚本](package-fpk.sh) |

改动前端后需重新构建二进制，才能验证内嵌页面。资源、API 和 WebSocket 地址都通过 `document.baseURI` 解析，以支持飞牛网关的子路径挂载。更多协作约定见 [AGENTS.md](AGENTS.md)。

## 安装和首次使用

1. 管理员进入飞牛「应用中心 → 手动安装」，选择与 NAS 架构对应的 FPK，按提示准备依赖应用 Node.js 24（`nodejs_v24`）。已有 1.x 安装可用同架构的新包升级。
2. 从飞牛桌面打开「Codex 工作台」。进入「版本管理」，启用本机已有的可信安装，或从 npm 安装最新版、历史版本或指定的完整版本号。图形工作台要求所选版本支持 App Server 协议。
3. 返回「工作台」，在「账号」中选择 ChatGPT 浏览器登录，打开授权链接。授权后，如果浏览器停在无法打开的本机回调页面，复制地址栏中的完整地址，粘回发起登录的工作台完成授权。也可选择设备登录或使用 OpenAI API 密钥，API 用量按 API 账户单独计费。选好工作区即可发送第一条消息。[Codex 登录说明](https://developers.openai.com/codex/auth/)
4. 在对话中查看回复及执行输出。当 Codex 发来审批请求时，可查看具体命令、路径或权限后作出决定。工作台支持中断任务和恢复历史对话；「终端」入口可打开 CLI 的原生交互界面。

「日志」页可查看本次服务启动后的运行记录和当前飞牛用户的登录诊断。点击导航栏的「GitHub」会在新标签页打开本项目。[项目仓库](https://github.com/boolerule/fnOS-codex)

### 请求设备码时出现 403

这表示 NAS 请求设备码时收到了拒绝响应。可直接在同一账号面板切换到 ChatGPT 浏览器登录，该流程使用浏览器授权码。交换授权码仍需 NAS 能够访问 OpenAI 的认证服务。要继续使用设备码，个人账号可在 ChatGPT 的「设置 → 安全」检查设备码登录开关，受管理的工作区请让管理员核对权限。若两种方式都失败，请查看「日志」中本次登录失败的步骤，并核对 NAS 到 `auth.openai.com` 的网络与代理。[OpenAI 无浏览器设备登录指南](https://developers.openai.com/codex/auth/#login-on-headless-devices)

### 本机版本扫描与切换

服务启动时在后台扫描本机。「版本管理」可离线查看已发现的版本、来源、可执行文件路径和当前启用项。相同版本装在多个位置时，可按路径选择；在 NAS 上另外安装或移除 Codex 后，点击「重新扫描」可刷新清单。

扫描覆盖工作台托管目录、服务 PATH、飞牛 Node.js 运行时、常见 npm 和 pnpm 全局目录、可读取的用户目录下的 NVM、fnm、asdf、mise、Volta 安装布局，以及 Codex 官方独立安装器的版本目录。自定义 `bin` 目录可通过 `CODEX_SCAN_PATHS` 补充。结果受应用账户的读取权限限制；其他用户可修改的安装仅供查看。

### 卸载和权限

卸载向导默认**保留系统 Codex**。选择同步清理后，工作台以普通应用账户重新核对安装位置，调用 npm 卸载有权移除的标准全局 `@openai/codex` 包。外部副本全部处理成功后，再清理工作台数据目录中的 `codex-cli/`。

对于受保护的全局 npm 安装，向导列出剩余路径和带实际 `--prefix` 的卸载命令，供安装所有者或系统管理员核对后执行。pnpm 管理的副本和独立二进制按其原安装方式处理。用户可在处理后重试，也可选择保留剩余系统安装并继续卸载工作台。CLI 清理仅操作程序包。应用数据目录中的登录、历史和默认工作区是否保留，取决于飞牛对应用数据的卸载处理。

## 网关、数据与本地调试

FPK 默认通过 `${TRIM_APPDEST}/app.sock`（0660）提供服务。飞牛将 `/app/CodexTerminal/` 下的 HTTP 和 WebSocket 请求转到同一 socket，并注入管理员身份。后端按当前 UID 查询已授权目录，在启动或恢复工作时校验项目路径。各管理员拥有独立的 Codex 配置、登录和会话目录，实际进程均以 `CodexTerminal` 应用账户运行。

FPK 的启动脚本将数据目录设置为 `${TRIM_PKGVAR}`，日志写入 `${TRIM_PKGVAR}/console.log`。下表中的独立监听地址和密码用于直接运行二进制；从应用中心启动时使用网关入口。[生命周期脚本](fnpack/cmd/main)

| 环境变量 | 用途 |
| --- | --- |
| `CODEX_CONSOLE_DATA` | 控制台数据目录。未设置时依次取 `HARNESS_DATA_DIR`、`TRIM_PKGVAR`、当前目录下的 `codex-data` |
| `CODEX_WORKSPACE` | 指向已有项目的绝对路径。留空时为每个身份在数据目录下创建工作区 |
| `CODEX_SCAN_PATHS` | 扩展扫描的绝对 `bin` 目录，Linux 下以冒号分隔，例如 `/opt/node/bin:/opt/tools/bin` |
| `CODEX_NPM_REGISTRY` | Codex 安装源，默认 `https://registry.npmjs.org/`；镜像需提供对应架构的包 |
| `CODEX_CONSOLE_SOCKET` / `CODEX_CONSOLE_BASE` | 覆盖网关 socket 和挂载前缀，兼容 `HARNESS_ADMIN_SOCK` / `HARNESS_ADMIN_BASEURL` |
| `CODEX_CONSOLE_LISTEN` / `CODEX_CONSOLE_PASSWORD` | 启用独立 HTTP 监听时同时设置，使用 HTTP Basic Auth |

托管版本保存在数据目录的 `codex-cli/<版本>/`，通过 `codex --version` 校验后才切换。每个身份的 CLI 登录和历史位于 `users/<飞牛 UID 或 local>/.codex/`，工作台的项目与对话索引位于同级的 `workbench/`。工作台重启会结束活动 PTY，已落盘的历史可在重新打开后读取。

在开发机上，以普通用户从仓库根目录执行以下命令，可用独立数据目录调试完整网页。需要试用已有项目时，将 `CODEX_WORKSPACE` 设为该项目的绝对路径。

```bash
make
export CODEX_CONSOLE_DATA="$HOME/.local/share/fnos-codex-dev"
export CODEX_CONSOLE_LISTEN="127.0.0.1:3079"
read -rsp '本次调试的访问密码：' CODEX_CONSOLE_PASSWORD
echo
export CODEX_CONSOLE_PASSWORD
./backend/codex-console
```

打开 [本机工作台](http://127.0.0.1:3079/)，使用用户名 `codex` 和刚设置的密码登录。若服务在远端 NAS 上运行，可在开发机执行 `ssh -N -L 3079:127.0.0.1:3079 用户名@NAS地址` 后打开同一地址。独立端口使用 `local` 身份，与飞牛网关登录分别保存数据。
