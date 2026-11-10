import { ref, type Ref } from 'vue'

export type Locale = 'zh' | 'en'

// 语言偏好只保存在本浏览器（localStorage），不随设置持久化到后端。
const LOCALE_KEY = 'console-language'

function readStoredLocale(): Locale {
  try {
    const v = localStorage.getItem(LOCALE_KEY)
    return isLocale(v || '') ? (v as Locale) : 'zh'
  } catch {
    // 隐私模式 / localStorage 被禁用：回退中文，不影响应用启动。
    return 'zh'
  }
}

// 当前语言（模块级共享，reactive）
const locale = ref<Locale>(readStoredLocale()) as Ref<Locale>

// <html lang> 跟随界面语言：读屏器、连字符与浏览器翻译都依赖它。
// 首屏由 index.html 的内联脚本先设，这里负责启动时对齐与切换时更新。
function applyDocumentLang(l: Locale) {
  if (typeof document !== 'undefined') {
    document.documentElement.lang = l === 'en' ? 'en' : 'zh-CN'
  }
}
applyDocumentLang(locale.value)

export function isLocale(v: string): v is Locale {
  return v === 'zh' || v === 'en'
}

const zh = {
  err_codex_version_invalid: "请选择有效的 Codex 版本号。",
  err_codex_install_request: "安装请求格式错误。",
  change_version: "选择版本",
  install_from_registry: "从 npm 安装",
  local_installations: "本机已安装",
  rescan_versions: "重新扫描",
  scanning_versions: "扫描中…",
  no_local_installations: "在已扫描的位置没有找到 Codex。",
  unknown_version: "版本未知",
  system_binary: "系统可执行文件",
  activate_installation: "启用",
  activate_installation_at: "启用 {version}（{path}）",
  read_only_installation: "仅供查看",
  version_management: "版本管理",
  choose_codex_version: "选择 Codex CLI 版本",
  search_version: "搜索或输入完整版本号",
  include_prereleases: "显示预发布版",
  latest_version: "最新版",
  install_latest: "安装最新版",
  install_version: "安装 {version}",
  switch_to_version: "切换到 {version}",
  current_version: "使用中",
  loading_versions: "正在获取版本列表…",
  versions_unavailable: "版本列表暂不可用。",
  retry_versions: "重试",
  more_versions: "显示更多版本",
  no_version_matches: "未找到匹配项，可输入完整版本号。",
  workbench: "工作台",
  go_install: "安装或更改 Codex 版本",
  cli_unavailable: "请到版本管理启用本机已有的 Codex，或安装新版本。",
  workspace_unavailable: "工作台连接失败",
  new_chat: "新对话",
  previous_chats: "历史对话",
  choose_chat: "选择历史对话",
  new_topic: "开始新对话",
  chat_intro: "选择工作区，发送第一条消息。",
  account: "账号",
  signed_out: "未登录",
  device_code: "设备码",
  open_signin: "打开授权页面",
  logout: "退出账号",
  model: "模型",
  default_model: "默认模型",
  cli_workspace: "工作目录",
  message_prompt: "输入给 Codex 的任务…",
  send: "发送",
  interrupt: "中断",
  older: "加载更早消息",
  agent_running: "Codex 正在执行",
  awaiting_approval: "等待你的审批",
  allow_once: "允许一次",
  deny: "拒绝",
  details: "查看详情",
  command_output: "命令输出",
  conversation_user: "你",
  file_changes: "文件变更",
  command_run: "执行命令",
  answer_request: "回答问题",
  send_answer: "提交回答",
  change_workspace: "管理授权目录",
  loading_workbench: "正在连接工作台…",
  sign_in_prompt: "请登录 Codex 后发送消息。",
  chat_history_empty: "暂无历史对话",
  other_active_chat: "另一个对话正在执行",
  show_active_chat: "查看进行中的对话",
  account_signin_failed: "未收到可用的设备登录地址，请重试。",
  browser_login: "使用 ChatGPT 登录",
  restart_browser_login: "重新发起 ChatGPT 登录",
  browser_callback_help: "在授权页面登录 ChatGPT。浏览器跳到 localhost 或 127.0.0.1 后，从地址栏复制完整网址并粘贴到下方，即使页面显示无法连接也可以复制。",
  browser_callback_label: "回调网址",
  complete_browser_login: "完成登录",
  waiting_browser_login: "已提交回调，正在等待 Codex 确认登录。",
  err_browser_login: "未能完成 ChatGPT 浏览器登录。请重新发起登录，并确认 NAS 能连接 OpenAI 的认证服务。",
  err_browser_login_busy: "NAS 的登录回调端口正在使用中。请在其他登录操作结束后重试。",
  err_login_expired: "这次登录已结束或已过期。请重新发起登录。",
  err_callback_invalid: "回调网址与本次登录不匹配。请复制本次授权后浏览器地址栏中的完整回调网址。",
  err_callback_failed: "未能完成本次回调。请重新发起 ChatGPT 登录。",
  err_device_login_failed: "设备登录未完成。请重试，或选择「使用 ChatGPT 登录」。",
  err_device_code_forbidden: "设备码请求被拒绝（HTTP 403）。可选择「使用 ChatGPT 登录」，通过浏览器授权。若继续用设备码，请在 ChatGPT「设置 → 安全」检查设备码登录权限，组织账号请联系工作区管理员。",
  err_api_key_login: "使用 API 密钥登录失败。请检查密钥并重试。",
  retry_device_login: "重试设备登录",
  device_login_help: "查看官方登录说明",
  api_key_login: "使用 API 密钥登录",
  api_key_label: "OpenAI API 密钥",
  api_key_billing: "使用此密钥的请求按 OpenAI API 用量单独计费。",
  auth_not_required: "当前配置无需登录 OpenAI。",
  workbench_error: "工作台请求失败",
  approval: "操作审批",
  resend_hint: "发送失败，草稿已保留。",
  submitted: "消息已发送",
  unavailable_model: "未能读取模型列表",
  additional_permission: "附加权限",
  please_select_workspace: "请选择工作区",
  err_origin_denied: '请求来源校验失败，请从控制台重新打开。',
  err_admin_required: '请使用飞牛管理员账号打开控制台。',
  err_login_required: '请重新登录控制台。',
  cancel: '取消',
  nav_overview: '概览',
  nav_terminal: '终端',
  nav_logs: '日志',
  log_scope: '显示本次服务启动以来的运行事件和当前账号的登录记录，按时间倒序排列。',
  log_empty: '暂无日志。',
  log_loading: '正在读取日志…',
  log_service_started: 'Codex 工作台服务已启动。',
  log_app_server_started: '当前用户的 Codex App Server 已启动。',
  log_app_server_start_failed: 'Codex App Server 启动失败，请检查当前选用的 Codex 安装。',
  log_app_server_exited: '当前用户的 Codex App Server 已退出。',
  log_device_code_forbidden: '请求设备码时收到 HTTP 403。',
  log_device_code_failed: '请求设备码失败。',
  log_device_code_issued: '已收到设备码，等待账号授权。',
  log_browser_auth_started: '浏览器登录已开始，等待授权回调。',
  log_browser_auth_failed: '无法启动浏览器登录。',
  log_browser_callback_rejected: '浏览器登录回调提交失败。',
  log_browser_callback_failed: 'Codex 未能完成已提交的浏览器授权回调。',
  log_browser_callback_forwarded: '已向 Codex 提交浏览器授权回调。',
  log_api_key_failed: 'Codex 拒绝了 API 密钥登录请求。',
  log_api_key_saved: 'Codex 已接受当前用户提交的 API 密钥。',
  log_account_login_completed: 'Codex 已确认账号登录成功。',
  log_account_login_failed: 'Codex 报告账号登录失败。',
  nav_github: 'GitHub',
  sidebar_expand: '展开',
  sidebar_collapse: '折叠',
  theme_light: '浅色',
  theme_dark: '深色',
  theme_system: '跟随系统',
  lang_zh: '中文',
  lang_en: 'English',
  dialog_close: '关闭',
  confirm_delete: '删除',
  directory_auth_success: '授权成功',
  directory_auth_failed: '目录授权失败，请重试',
  directory_appid_missing: '无法获取应用标识，请稍后重试',
  directory_pick_title: '选择授权目录',
  directory_pick_ok: '确认授权',
  directory_host_bridge_lost: '与飞牛桌面的连接已失效，请关闭本应用窗口后重新打开（刷新页面无效）',
  term_reconnect: '重连',
  term_clear: '清空',
  term_paste: '粘贴',
  term_copied: '已复制到剪贴板',
  term_not_connected: '终端未连接',
  term_conn_closed: '连接已关闭。点击重连按钮或刷新页面重连。',
  term_ws_error: 'WebSocket 错误。',
  term_conn_detached: '会话已在其他设备打开',
  term_conn_detached_hint: '该会话已在其他设备打开，点击「重连」可在此设备继续。',
  keypad_prev_page: '上一页',
  keypad_next_page: '下一页',
  console_title: "Codex 控制台",
  refresh: "刷新",
  status_unknown: "尚未取得状态",
  installing: "正在安装…",
  install: "安装 Codex",
  upgrade: "安装最新版",
  installed: "已安装",
  not_installed: "尚未安装",
  system_install: "系统安装",
  managed_install: "控制台管理",
  workspace: "工作区",
  no_workspaces: "当前没有可用工作区。",
  authorize: "授权目录",
  start_codex: "启动 Codex",
  device_login: "设备登录",
  sessions: "终端会话",
  no_sessions: "还没有终端会话。",
  end_session: "结束会话",
  delete_session: "删除会话",
  end_session_hint: "将关闭此终端并移除其回放记录。\n{path}",
  running: "运行中",
  exited: "已退出",
  resume: "进入会话",
  view_output: "查看输出",
  select_session: "从概览页选择工作区启动 Codex，或进入已有会话。",
  terminal_disabled: "此构建已停用网页终端。",
  terminal_title: "Codex 终端",
}

const en: Record<keyof typeof zh, string> = {
  err_codex_version_invalid: "Choose a valid Codex version number.",
  err_codex_install_request: "Invalid installation request.",
  change_version: "Change version",
  install_from_registry: "Install from npm",
  local_installations: "Local installations",
  rescan_versions: "Rescan",
  scanning_versions: "Scanning…",
  no_local_installations: "No Codex installations found in the scanned locations.",
  unknown_version: "Unknown version",
  system_binary: "System executable",
  activate_installation: "Activate",
  activate_installation_at: "Activate {version} at {path}",
  read_only_installation: "View only",
  version_management: "Versions",
  choose_codex_version: "Choose a Codex CLI version",
  search_version: "Search or enter an exact version",
  include_prereleases: "Include prereleases",
  latest_version: "Latest",
  install_latest: "Install latest",
  install_version: "Install {version}",
  switch_to_version: "Switch to {version}",
  current_version: "Active",
  loading_versions: "Loading versions…",
  versions_unavailable: "Version list unavailable.",
  retry_versions: "Retry",
  more_versions: "Show more versions",
  no_version_matches: "No matches. Enter an exact version number to install it.",
  workbench: "Workbench",
  go_install: "Install or change the Codex version",
  cli_unavailable: "Open Versions to activate a local Codex installation or install a new version.",
  workspace_unavailable: "Workbench connection failed",
  new_chat: "New chat",
  previous_chats: "Conversations",
  choose_chat: "Select a conversation",
  new_topic: "Start a conversation",
  chat_intro: "Choose a workspace and send your first message.",
  account: "Account",
  signed_out: "Signed out",
  device_code: "Device code",
  open_signin: "Open sign-in page",
  logout: "Sign out",
  model: "Model",
  default_model: "Default model",
  cli_workspace: "Working directory",
  message_prompt: "Send a task to Codex…",
  send: "Send",
  interrupt: "Interrupt",
  older: "Load earlier messages",
  agent_running: "Codex is working",
  awaiting_approval: "Waiting for your approval",
  allow_once: "Allow once",
  deny: "Decline",
  details: "View details",
  command_output: "Command output",
  conversation_user: "You",
  file_changes: "File changes",
  command_run: "Command",
  answer_request: "Answer questions",
  send_answer: "Submit answers",
  change_workspace: "Manage authorized folders",
  loading_workbench: "Connecting to the workbench…",
  sign_in_prompt: "Sign in to Codex to send messages.",
  chat_history_empty: "No conversations yet",
  other_active_chat: "Another conversation is running",
  show_active_chat: "Open active conversation",
  account_signin_failed: "No device sign-in URL was returned. Try again.",
  browser_login: "Sign in with ChatGPT",
  restart_browser_login: "Restart ChatGPT sign-in",
  browser_callback_help: "Sign in to ChatGPT on the authorization page. When the browser reaches localhost or 127.0.0.1, copy the full URL from the address bar and paste it below. You can copy it even if the page cannot connect.",
  browser_callback_label: "Callback URL",
  complete_browser_login: "Complete sign-in",
  waiting_browser_login: "Callback submitted. Waiting for Codex to confirm sign-in.",
  err_browser_login: "ChatGPT browser sign-in could not finish. Start again and check that the NAS can reach OpenAI authentication services.",
  err_browser_login_busy: "The NAS sign-in callback port is in use. Try again after the other sign-in finishes.",
  err_login_expired: "This sign-in has ended or expired. Start a new sign-in.",
  err_callback_invalid: "The callback URL does not match this sign-in. Copy the full URL from the address bar after authorizing this attempt.",
  err_callback_failed: "The callback could not be completed. Start a new ChatGPT sign-in.",
  err_device_login_failed: "Device sign-in did not complete. Try again or choose Sign in with ChatGPT.",
  err_device_code_forbidden: "The device code request was denied (HTTP 403). Choose Sign in with ChatGPT to authorize in your browser. For device sign-in, check Settings → Security in ChatGPT or ask your workspace administrator to check permissions.",
  err_api_key_login: "API key sign-in failed. Check the key and try again.",
  retry_device_login: "Retry device sign-in",
  device_login_help: "Read the official sign-in guide",
  api_key_login: "Sign in with an API key",
  api_key_label: "OpenAI API key",
  api_key_billing: "Requests using this key are billed separately to your OpenAI API account.",
  auth_not_required: "Your current configuration does not require OpenAI sign-in.",
  workbench_error: "Workbench request failed",
  approval: "Approval requested",
  resend_hint: "Send failed. Your draft has been kept.",
  submitted: "Message sent",
  unavailable_model: "Could not load models",
  additional_permission: "Additional permissions",
  please_select_workspace: "Select a workspace",
  err_origin_denied: 'Request origin rejected. Reopen the console and try again.',
  err_admin_required: 'Open the console with a fnOS administrator account.',
  err_login_required: 'Sign in to the console again.',
  cancel: 'Cancel',
  nav_overview: 'Overview',
  nav_terminal: 'Terminal',
  nav_logs: 'Logs',
  log_scope: 'Service events and your sign-in history since the last service start, newest first.',
  log_empty: 'No log entries.',
  log_loading: 'Loading logs…',
  log_service_started: 'Codex Console started.',
  log_app_server_started: 'Your Codex App Server started.',
  log_app_server_start_failed: 'Could not start Codex App Server. Check the selected Codex installation.',
  log_app_server_exited: 'Your Codex App Server exited.',
  log_device_code_forbidden: 'Device code request was refused (HTTP 403).',
  log_device_code_failed: 'Device code request failed.',
  log_device_code_issued: 'Device code was issued. Waiting for authorization.',
  log_browser_auth_started: 'Browser sign-in started. Waiting for the callback.',
  log_browser_auth_failed: 'Could not start browser sign-in.',
  log_browser_callback_rejected: 'Browser sign-in callback could not be accepted.',
  log_browser_callback_failed: 'Codex could not complete the submitted browser callback.',
  log_browser_callback_forwarded: 'Browser sign-in callback submitted to Codex.',
  log_api_key_failed: 'Codex rejected API key sign-in.',
  log_api_key_saved: 'Codex accepted the API key for this account.',
  log_account_login_completed: 'Codex confirmed account sign-in.',
  log_account_login_failed: 'Codex reported that account sign-in failed.',
  nav_github: 'GitHub',
  sidebar_expand: 'Expand',
  sidebar_collapse: 'Collapse',
  theme_light: 'Light',
  theme_dark: 'Dark',
  theme_system: 'System',
  lang_zh: '中文',
  lang_en: 'English',
  dialog_close: 'Close',
  confirm_delete: 'Delete',
  directory_auth_success: 'Authorization succeeded',
  directory_auth_failed: 'Directory authorization failed. Please try again.',
  directory_appid_missing: 'Unable to get app ID, please try again',
  directory_pick_title: 'Select directory to authorize',
  directory_pick_ok: 'Confirm authorization',
  directory_host_bridge_lost: 'Connection to the fnOS desktop was lost. Close and reopen this app window (reloading the page does not help).',
  term_reconnect: 'Reconnect',
  term_clear: 'Clear',
  term_paste: 'Paste',
  term_copied: 'Copied to clipboard',
  term_not_connected: 'Terminal not connected',
  term_conn_closed: 'Connection closed. Click reconnect or refresh the page.',
  term_ws_error: 'WebSocket error.',
  term_conn_detached: 'Session opened on another device',
  term_conn_detached_hint: 'This session is now open on another device. Click Reconnect to continue here.',
  keypad_prev_page: 'Previous page',
  keypad_next_page: 'Next page',
  console_title: "Codex Console",
  refresh: "Refresh",
  status_unknown: "Status unavailable",
  installing: "Installing…",
  install: "Install Codex",
  upgrade: "Install latest version",
  installed: "Installed",
  not_installed: "Not installed",
  system_install: "System installation",
  managed_install: "Console-managed",
  workspace: "Workspace",
  no_workspaces: "No workspaces are available.",
  authorize: "Authorize folder",
  start_codex: "Start Codex",
  device_login: "Device sign-in",
  sessions: "Terminal sessions",
  no_sessions: "No terminal sessions yet.",
  end_session: "End session",
  delete_session: "Delete session",
  end_session_hint: "Close this terminal and remove its replay history?\n{path}",
  running: "Running",
  exited: "Exited",
  resume: "Open session",
  view_output: "View output",
  select_session: "Start Codex in a workspace or open an existing session from Overview.",
  terminal_disabled: "The web terminal is disabled in this build.",
  terminal_title: "Codex terminal",
}

const dict: Record<Locale, Record<string, string>> = { zh, en }

/** 前端界面文案的合法 key（由中文词典推导）。 */
export type I18nKey = keyof typeof zh

export function setLocale(l: Locale) {
  locale.value = l
  applyDocumentLang(l)
  try {
    localStorage.setItem(LOCALE_KEY, l)
  } catch {
    /* 隐私模式：本次会话内仍生效 */
  }
}

// tr 是本模块的翻译实现：useI18n().t 与下面的 uiText / uiErrText 共用。
// 取不到 key 时返回 key 本身（调用方据此判断「这条没有译文」）。
function tr(key: string, params?: Record<string, string | number>): string {
  let s = dict[locale.value][key]
  if (s === undefined) s = dict.zh[key]
  if (s === undefined) s = key
  if (params) {
    for (const k of Object.keys(params)) {
      s = s.replace(new RegExp(`\\{${k}\\}`, 'g'), () => String(params[k]))
    }
  }
  return s
}

// 对外的 t 只接受字典里真实存在的 key：拼错、改名漏改都会在 vue-tsc 阶段暴露。
// 后端下发的动态 code 走下面的 uiText/uiErrText（它们内部用 tr，允许未知 code 回退原文）。
function t(key: I18nKey, params?: Record<string, string | number>): string {
  return tr(key, params)
}

export function useI18n() {
  return { locale, t, setLocale }
}

// --- 后端返回文案的本地化 ---
//
// 后端返回的是「原文 + code + 参数」：有 code 且字典命中就用当前
// 语言的译文，否则回退到后端给的中文原文。这样 toast / 进度行 / 弹窗诊断都随语言切换，
// 而旧版前端遇到新 code 也只是显示原文，不会白屏或显示 key。

// UIRefLike 是后端文案引用的结构（与 serverapi 的 UIRef 一致，这里只做结构约束，
// 避免 composable 反向依赖 serverapi）。
export interface UIRefLike {
  code?: string
  params?: Record<string, string>
}

// uiText 渲染一条「原文 + 引用」：优先译文，其次原文，最后 fallback。
export function uiText(text?: string, ref?: UIRefLike, fallback = ''): string {
  if (ref?.code) {
    const s = tr(ref.code, ref.params)
    if (s !== ref.code) return s
  }
  return text || fallback
}

// uiErrText 渲染一个抛出的错误（通常是 serverapi 的 ApiError）。
export function uiErrText(e: unknown, fallback = ''): string {
  const err = e as { code?: string; params?: Record<string, string>; message?: string } | null
  return uiText(err?.message, err ?? undefined, fallback)
}
