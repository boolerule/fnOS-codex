# fnOS Codex 控制台协作约定

本项目在 fnOS 上提供 Codex 网页工作台、版本管理及可选终端。默认进入图形工作台，Go 后端通过官方 App Server 的 stdio JSONL 协议维护对话、工具输出和审批。项目的运行与构建入口见 [README](README.md)。

## 入口和边界

- `backend/main.go` 启动 Unix 网关与可选的独立 HTTP 监听，退出时关闭会话与安装任务。`console.go` 负责身份、来源校验和管理路由。飞牛身份头只可信于 Unix socket，独立端口使用自己的密码。所有网页/API/WebSocket 入口均须经过同一鉴权。
- `workspace.go` 根据配置和当前飞牛 UID 的授权目录生成可用工作区。创建和重新挂载终端时由服务端核验目录。不同管理员有独立的 CLI home 和会话归属，实际 Linux 进程共享应用系统账户。
- `codex_install.go` 在应用私有目录安装官方 `@openai/codex`。先写临时目录、验证二进制，再原子切换活动版本。安装失败保留旧安装，使用绝对路径执行程序。
- 本机版本发现与 npm 网络目录分开。系统副本以规范化后的可执行路径标识；相同版本号可以对应不同安装。由其他用户控制的安装只能读取元数据。保留旧版 `current` 的纯版本号格式兼容性。
- 飞牛卸载向导默认保留系统副本。同步清理仅在明确选择的卸载操作中执行，升级流程不可触发。删除前重新核对包清单、命令链接与权限；用隔离的 npm 前缀验证，勿以开发机的实际全局安装作为测试目标。
- `workbench.go` 按已认证身份创建独立工作台。每位用户的 PTY 与图形工作台共享 Codex 登录。App Server 的子进程环境必须通过白名单构造，所有任务必须复核当前工作区授权；`internal/workbench` 负责对话状态、审批与重连。
- `terminal.go` 直接启动交互式 `codex` 或 `codex login --device-auth`。进程参数与环境使用显式白名单。登录文件由 Codex 自己维护。不要把控制台密码或飞牛平台令牌传给 CLI。
- 终端断线只解除挂载。重连要回放历史，每个会话同时只有一个输入端。终端 API 在 `terminal_routes.go` 注册，并检查当前身份。仅允许已认证的所有者访问历史或结束会话。
- 后端日志一律使用 `logInfo` / `logWarn` / `logError`，诊断日志使用英文。终端输出只发往该用户的会话。

## 前端与构建

Vue 3 组件使用 Composition API、`<script setup>` 和 TypeScript。页面通过 `?view=` 切换，支持移动端键盘与会话重连。语言和主题为本机偏好。面向用户的代码化提示需补中英译文。

Vite 使用相对 `base: './'`。Go 在运行时注入实际挂载路径，前端用 `document.baseURI` 解析资源、API 和 WebSocket。飞牛宿主 SDK 在入口尽早初始化，以便目录授权在已打开的标签页里正常工作。

`make` 和 `bash build.sh` 走同一构建链，先构建前端，再嵌入 `backend/embed` 并编译 Go。改前端后要重新构建，并用实际 URL 验证。`TERMINAL=0` 同时设置 Go 的 `noterminal` 标签及前端 `VITE_TERMINAL=0`。默认 Go 缓存位于仓库内。

涉及终端的改动应在 Linux 验证真实 PTY、断开重连和显式结束。涉及鉴权和目录的改动要覆盖密码入口、网关入口、跨用户会话和未授权路径。
