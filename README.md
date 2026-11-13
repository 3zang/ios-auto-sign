# iOS 自动签名 · ios-auto-sign

自托管的 IPA 自动重签名与 OTA 分发平台。订阅软件源一旦发布新版本，平台会按你为每个应用设定的名称、图标和证书自动完成重签名、发布安装包，并推送更新提醒。整个过程不需要 Mac、不需要 Xcode、不需要第三方签名服务。

前后端打包为**一个 Go 二进制文件**（前端通过 `go:embed` 内嵌），仅依赖一个外部的 [zsign](https://github.com/zhlynn/zsign) 可执行文件，支持 Windows、Linux amd64 与 Linux arm64。

---

## 目录

- [功能特性](#功能特性)
- [工作原理](#工作原理)
- [技术架构](#技术架构)
- [目录结构](#目录结构)
- [快速开始](#快速开始)
- [配置说明](#配置说明)
- [使用流程](#使用流程)
- [安装链路与 plist 模式](#安装链路与-plist-模式)
- [设置中心与 Bark 通知](#设置中心与-bark-通知)
- [HTTP API](#http-api)
- [数据存储与安全](#数据存储与安全)
- [多平台编译](#多平台编译)
- [部署建议](#部署建议)
- [常见问题](#常见问题)
- [已知限制](#已知限制)
- [第三方组件](#第三方组件)

---

## 功能特性

| 分类 | 能力 |
| --- | --- |
| 账号 | 注册 / 登录 / 会话 Cookie；**首个注册的账号自动成为管理员**；普通用户只能看到自己的产物、证书与配置 |
| 订阅源 | 可配置多个 AltStore 格式软件源；ETag + 响应体 SHA256 双重去重；每个源可单独设置轮询间隔、启用 / 停用、立即执行 |
| 应用元数据 | 拉取 `all-apps.json` 中的应用名称、bundle id、图标、开发者、版本列表（版本 / 日期 / 下载地址 / 大小 / 最低系统版本） |
| 个性化配置 | 按 bundle id 自定义显示名称、图标来源（订阅源原始图标 / 管理员图标库 / 自定义 URL）、bundle id 覆盖、签名参数；配置持久化 |
| 图标库 | 管理员维护多个图标 URL；列表缩略图预览、点击放大预览、添加时实时预览；可供所有用户选择 |
| 证书 | 上传 `.p12` + 证书密码 + `.mobileprovision` 三件套，AES-256-GCM 加密落盘，接口永不返回明文 |
| 自动签名 | 检测到新版本后按应用创建串行签名任务，调用 zsign；失败冷却与最多 3 次自动重试；任务记录只保留最近 5 条 |
| 版本保留 | 按「用户 + 应用」只保留最近 N 个成功产物（默认 2），同时清理 IPA 与图标文件 |
| 安装分发 | 内置 plist 生成器或远程 `/genPlist` 服务；安装页显示图标、版本、更新时间、大小、安装按钮与二维码；二维码离线生成 |
| 账号隔离 | `/api/install/apps` 与独立 `/install` 页均需登录，普通用户只看自己的产物 |
| 通知 | Bark 更新推送，按应用逐条发送「XX 有更新」；首次导入订阅源静默，避免历史版本刷屏 |
| 界面 | Apple 风格响应式界面、深色 / 浅色主题切换、可自定义站点标题、移动端适配、PC 端居中布局 |

---

## 工作原理

```
订阅源 all-apps.json
        │  ETag + SHA256 变更检测（首次导入静默）
        ▼
   版本目录写入 store.json ──► 按应用触发 Bark 通知
        │
        ▼  仅对「已启用且有配置」的应用
   创建签名任务（串行队列）
        │
        ├─ 下载原始 IPA
        ├─ 解密 p12 / 密码 / mobileprovision
        ├─ 生成自定义图标文件
        └─ zsign -k ... -p ... -m ... -I ... -o signed.ipa unsigned.ipa
        │
        ▼
   产物发布（IPA + 图标 + 元数据）──► 按应用保留最近 N 个版本
        │
        ▼
   安装页 /api/install/apps ──► itms-services 安装链接 + 二维码
```

HTTP 层在**每次请求时**根据当前 `Host` / `X-Forwarded-Host` / `X-Forwarded-Proto` / TLS 状态推导对外地址，因此用局域网 IP、localhost、域名或反向代理访问时，IPA 与 `fetchurl` 地址都会自动跟着变，不需要在配置里写死域名。

---

## 技术架构

- **语言 / 依赖**：Go 1.22+ 语法目标，零第三方 Go 模块，全部使用标准库。
- **前端**：原生 HTML / CSS / JavaScript，通过 `go:embed static/*` 内嵌，无构建步骤。
- **存储**：单文件 `data/store.json`，`Store.Update` 带内存快照回滚（写盘失败不会留下脏数据），`Update` / `View` 是未来替换为 SQLite 的接口缝。
- **加密**：PBKDF2-HMAC-SHA256 密码哈希；AES-256-GCM 加密证书三件套；主密钥来自 `IOS_SIGN_MASTER_KEY` 环境变量或 `data/master.key`。
- **会话**：随机 token + `ias_session` Cookie；当 `public_base_url` 为 `https://` 时 Cookie 带 `Secure`。
- **签名**：外部进程 zsign，Windows 默认 `bin/zsign.exe`，Linux 默认 `bin/zsign`。

| 包 | 职责 |
| --- | --- |
| `cmd/server` | 入口、目录初始化、签名队列、轮询循环、HTTP Server、优雅退出 |
| `internal/config` | 配置结构、默认值、环境变量覆盖、跨平台路径归一 |
| `internal/store` | 数据模型、JSON 原子持久化、运行时设置 |
| `internal/secure` | 密码哈希、AES-GCM、主密钥、随机 token |
| `internal/auth` | 注册 / 登录 / 会话 / Cookie / 权限包装 |
| `internal/source` | 订阅源解析、ETag + SHA256 去重、按应用更新事件 |
| `internal/plist` | plist 生成器抽象、内置生成器、远程 `/genPlist` URL 拼装 |
| `internal/signing` | 任务队列、下载、解密、调用 zsign、产物发布、保留策略 |
| `internal/notify` | Bark 推送 |
| `internal/web` | HTTP API、安装数据、manifest / IPA / 图标端点、页面服务 |

---

## 目录结构

```
.
├── cmd/server/main.go            # 程序入口
├── internal/
│   ├── auth/                     # 账号与会话
│   ├── config/                   # 配置
│   ├── notify/                   # Bark 通知
│   ├── plist/                    # plist 生成器
│   ├── secure/                   # 加密与密钥
│   ├── signing/                  # 签名队列
│   ├── source/                   # 订阅源同步
│   ├── store/                    # 持久化
│   └── web/
│       ├── web.go                # 路由与处理器
│       └── static/               # 内嵌前端
│           ├── index.html        # 控制台（含登录 / 注册）
│           ├── install.html      # 独立安装页
│           ├── app.js / style.css / theme.js
│           ├── qrcode.js         # 离线二维码库
│           └── favicon.svg
├── config.example.json           # 配置模板
├── go.mod
└── README.md
```

运行时会自动创建（**不要提交到仓库**）：

```
data/
├── master.key        # 主密钥，丢失后已存证书无法解密，务必备份
├── store.json        # 全部业务数据
├── artifacts/        # 签名后的 IPA
├── assets/           # 图标文件
├── manifests/        # 内置模式生成的 plist
└── tmp/              # 签名临时目录
bin/
└── zsign[.exe]       # 需要自行放置的外部签名程序
```

---

## 快速开始

### 1. 获取程序

可以直接使用 Release 中的预编译二进制，或从源码构建：

```bash
git clone https://github.com/<your-account>/ios-auto-sign.git
cd ios-auto-sign
go build -trimpath -ldflags "-s -w" -o ios-auto-sign ./cmd/server
```

Windows 下产物为 `ios-auto-sign.exe`。

### 2. 放置 zsign（必须）

签名依赖 [zsign](https://github.com/zhlynn/zsign) 的预编译可执行文件（本项目不含该二进制，也不在仓库中分发）：

| 平台 | 放置路径 |
| --- | --- |
| Windows | `bin/zsign.exe` |
| Linux amd64 | `bin/zsign` |
| Linux arm64 | `bin/zsign`（需为 arm64 构建） |

Linux 上记得加执行权限：`chmod +x bin/zsign`。配置文件 `zsign_path` 可以指向任意绝对路径。

> 若启动时日志出现 `WARNING: zsign not found`，同步与界面仍可正常使用，但签名任务会失败。

### 3. 准备配置（可选）

程序在没有 `config.json` 时直接使用默认值。需要自定义时：

```bash
cp config.example.json config.json
```

然后按下面的「配置说明」修改。**`config.json` 已被 `.gitignore` 忽略，不要提交。**

### 4. 启动

```bash
# Linux / macOS
./ios-auto-sign

# Windows
ios-auto-sign.exe
```

默认监听 `127.0.0.1:8080`，浏览器打开 `http://127.0.0.1:8080`。要允许局域网设备访问，把 `listen` 改成 `0.0.0.0:8080`（或用 `IOS_SIGN_LISTEN`）。

首次打开会进入登录页，**第一个注册的账号即为管理员**（用户名 3–64 字符，密码不少于 8 位）。程序启动时若没有任何订阅源，会自动添加默认的 [braless/soft_collections](https://github.com/braless/soft_collections) 源。

### 5. 最短可用路径

1. 注册管理员账号并登录。
2. 「证书」页上传 `.p12` + 密码 + `.mobileprovision`。
3. 「订阅源」点「立即执行」拉取元数据。
4. 「应用与配置」找到目标应用 → 展开「配置签名元数据」→ 勾选「启用自动签名」、选好证书、（可选）改名称与图标 → 保存。
5. 点「立即签名」或等待轮询；「任务与产物」出现成功记录后，「安装」页即可看到安装按钮与二维码。

---

## 配置说明

配置优先级：**内置默认值 < `config.json` < 环境变量**。`config.json` 路径由 `-config` 或 `IOS_SIGN_CONFIG` 指定，默认读当前工作目录下的 `config.json`；文件不存在不是错误。

### config.json 字段

| 字段 | 默认值 | 说明 |
| --- | --- | --- |
| `listen` | `127.0.0.1:8080` | HTTP 监听地址 |
| `public_base_url` | `http://127.0.0.1:8080` | 现在**只用于决定会话 Cookie 是否带 `Secure`**；安装链接地址已改为按当前请求动态生成 |
| `data_dir` | `data` | 数据目录（会转成绝对路径） |
| `master_key_file` | `master.key` | 主密钥文件；相对路径时相对于 `data_dir` |
| `zsign_path` | `bin/zsign` / `bin/zsign.exe` | zsign 可执行文件路径 |
| `poll_interval_minutes` | `30` | 轮询间隔，最小 1 |
| `keep_versions` | `2` | 每个应用保留的成功产物数，最小 1 |
| `http_timeout_seconds` | `60` | 外部 HTTP 请求超时，最小 5 |
| `default_source_url` | braless `all-apps.json` | 首次启动自动添加的订阅源 |
| `plist.mode` | `builtin` | `builtin` 或 `plistserver` |
| `plist.plistserver_url` | `http://127.0.0.1:3788` | 远程 `/genPlist` 服务地址，可写完整 `.../genPlist` |
| `plist.ipa_base_url` | 空 | 旧配置兼容字段；当前 HTTP 层不再依赖它生成地址 |
| `sign_options.zip_level` | `9` | zsign 压缩级别 0–9 |
| `sign_options.force` | `false` | 等同 zsign `-f` |
| `sign_options.adhoc` | `false` | 等同 zsign `-a`，不使用证书 |
| `sign_options.extra_args` | `[]` | 追加给 zsign 的额外参数 |

### 环境变量

| 变量 | 对应字段 |
| --- | --- |
| `IOS_SIGN_CONFIG` | 配置文件路径 |
| `IOS_SIGN_LISTEN` | `listen` |
| `IOS_SIGN_PUBLIC_BASE_URL` | `public_base_url` |
| `IOS_SIGN_DATA_DIR` | `data_dir` |
| `IOS_SIGN_MASTER_KEY` | 直接提供主密钥（不再读取 `master.key` 文件） |
| `IOS_SIGN_MASTER_KEY_FILE` | `master_key_file` |
| `IOS_SIGN_ZSIGN` | `zsign_path` |
| `IOS_SIGN_DEFAULT_SOURCE` | `default_source_url` |
| `IOS_SIGN_PLIST_MODE` | `plist.mode` |
| `IOS_SIGN_PLISTSERVER_URL` | `plist.plistserver_url` |
| `IOS_SIGN_PLIST_IPA_BASE_URL` | `plist.ipa_base_url` |
| `IOS_SIGN_POLL_MINUTES` | `poll_interval_minutes` |
| `IOS_SIGN_KEEP_VERSIONS` | `keep_versions` |
| `IOS_SIGN_HTTP_TIMEOUT` | `http_timeout_seconds` |

> **路径解析**：`config.json`、`data/`、`bin/` 都相对**进程当前工作目录**。请从程序所在目录启动，或在配置里使用绝对路径 / 环境变量。

---

## 使用流程

### 订阅源

- 支持多个 AltStore 格式 JSON 源，可分别设定名称、URL、轮询间隔、启用状态。
- 同步使用 ETag + 响应体 SHA256 双重去重：内容未变但文件被重新生成（例如 CI 定时重跑）不会误判为新版本。
- 列表里「刷新 / 启用·停用 / 删除」并列；「立即执行」会同步所有启用源、创建待签名任务并返回新增版本数。
- **首次导入某个源时不会推送历史版本通知**，只有之后真正新增的版本才会触发 Bark。

### 应用与配置

- 「已配置 / 全部」分段筛选；支持「按时间」（最新版本日期倒序）与「按名称」排序。
- 每个应用可配置：启用自动签名、显示名称、图标来源、图标库选择、自定义图标 URL、证书、bundle id 覆盖、压缩级别、强制签名、ad-hoc 模式。
- 自定义图标会通过 `zsign -I` 写进 IPA，因此安装到设备后的桌面图标也会改变。
- 只有「已启用 + 已保存配置」的应用才会在检测到新版本时自动创建签名任务；也可以在卡片里点「立即签名」手动触发。

### 图标库

- 管理员添加图标 URL 供所有用户选择；列表显示缩略图，点击可放大预览并打开原图。
- 添加表单在输入 URL 时会实时预览，加载失败自动隐藏预览框。

### 证书

- 正常重签名需要三件套：`.p12`、证书密码、`.mobileprovision`。仅 p12 + 密码无法完成常规签名。
- 三者均以 AES-256-GCM 加密存储，列表接口只返回名称、创建时间与「是否完整」，不会返回明文。
- ad-hoc 模式不需要证书，但适用范围有限。

### 任务与产物

- 「任务与产物」页上方是「已发布产物」，下方是「签名任务」。
- 签名任务记录只保留最近 5 条；产物按「用户 + 应用」保留最近 `keep_versions` 个版本，超出时 IPA 与图标文件一并清理。
- 产物列表只提供一个「下载」链接，直接指向签名后的 IPA。

### 安装

- 控制台「安装」标签页与独立 `/install` 页展示同样的数据，两者都要求登录并按账号隔离。
- 每张卡片显示图标、版本、更新时间、大小，以及「安装」按钮和「二维码」按钮。
- 二维码在浏览器本地生成（不依赖网络），内容为 `itms-services` 安装链接；弹窗支持复制链接。
- 请使用 **iOS 设备的 Safari** 打开安装链接；桌面浏览器点击不会触发安装。

---

## 安装链路与 plist 模式

### itms-services 链接

```
itms-services://?action=download-manifest&url=<manifest URL>
```

`url=` 后面的整个 manifest 地址会作为**一个整体**做百分号编码，避免内层的 `?`、`&` 被误当成 itms-services 自身的查询参数。

### builtin 模式（默认）

服务端按需生成 plist，包含 `software-package`、`display-image`、`full-size-image` 资产：

- manifest：`/manifest/{id}.plist`
- IPA：`/ipa/{id}.ipa`
- 图标：`/assets/{name}`

### plistserver 模式

配置为远程 `/genPlist` 服务（例如 `https://api.palera.in/genPlist`）时，服务端不再本地生成或存储 plist，而是把 iOS 直接指向远程地址：

```
https://api.palera.in/genPlist?bundleid=<bundle id>&fetchurl=<URL 编码后的 IPA 地址>&name=<名称>&version=<版本>
```

编码分两层：

1. `fetchurl` 等查询值逐项严格编码（`:` `/` `?` `&` `#` 空格全部转义，空格为 `%20`）；
2. 拼好的整个 manifest URL 再整体编码后放进 `itms-services` 的 `url=` 里。

`fetchurl` 中的 IPA 地址由当前请求的 Host 实时生成，因此换 IP、换域名、走反向代理都不需要改配置。

### HTTPS 要求（重要）

iOS 的 OTA 安装要求 **manifest、IPA、图标 URL 全部通过受信任的 HTTPS 提供**。局域网 HTTP 地址通常可以生成链接，但设备会拒绝安装。生产部署请配置域名与证书（例如 Caddy 自动签发），并通过反向代理把 `X-Forwarded-Proto: https` 和 `X-Forwarded-Host` 传给本服务。

---

## 设置中心与 Bark 通知

设置中心（仅管理员可修改）持久化在 `store.json` 的 `settings` 段，不写 `config.json`：

| 项 | 说明 |
| --- | --- |
| 站点标题 | 自定义控制台左上角名称，保存后立即生效，同时更新浏览器标题 |
| Bark 开关 | 关闭后不发送任何推送；「发送测试推送」无论开关状态都会强制发一条 |
| 服务器地址 | 例如 `https://api.day.app` |
| 设备 Key | 推送地址最后一段；普通用户读取设置时**不会返回该字段** |
| 通知分组 / 提示音 | 可选 |

推送优先 `POST {server}/{key}`，失败回退 `POST {server}/push`（body 带 `device_key`），10 秒超时。检测到新版本时按应用发送「{应用名} 有更新」，正文包含版本号与来源。通知失败只写日志，不影响同步主流程。

右上角开关可切换深色 / 浅色主题，偏好保存在浏览器 `localStorage`；未手动选择时跟随系统 `prefers-color-scheme`。

---

## HTTP API

所有 `/api/*` 接口除认证与公开配置外都需要登录（`ias_session` Cookie）。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `POST` | `/api/auth/register` | 注册（首个账号为 admin），成功后自动登录 |
| `POST` | `/api/auth/login` | 登录 |
| `POST` | `/api/auth/logout` | 退出 |
| `GET` | `/api/me` | 当前用户 |
| `GET` | `/api/config/public` | 公开配置（标题、地址、模式、保留数等） |
| `GET` / `POST` | `/api/sources` | 订阅源列表 / 新增 |
| `PUT` / `DELETE` | `/api/sources/{id}` | 修改（含启用停用）/ 删除 |
| `POST` | `/api/sources/{id}/refresh` | 立即同步单个源 |
| `POST` | `/api/sync/run` | 立即同步所有启用源并创建签名任务 |
| `GET` | `/api/apps` | 应用元数据 + 版本 + 当前用户配置 |
| `PUT` | `/api/apps/{id}/config` | 保存应用签名配置 |
| `POST` | `/api/apps/{id}/sign` | 手动创建签名任务 |
| `GET` / `POST` | `/api/icons` | 图标库列表 / 添加（管理员） |
| `DELETE` | `/api/icons/{id}` | 删除图标（管理员） |
| `GET` / `POST` | `/api/certificates` | 证书列表 / 上传（`multipart/form-data`） |
| `DELETE` | `/api/certificates/{id}` | 删除证书 |
| `GET` | `/api/jobs` | 签名任务（最近 5 条） |
| `GET` | `/api/artifacts` | 已发布产物（账号隔离） |
| `GET` / `PUT` | `/api/settings` | 读取 / 保存全局设置（写入仅管理员） |
| `POST` | `/api/settings/bark/test` | 发送测试推送 |
| `GET` | `/api/install/apps` | 安装数据（需登录、账号隔离）；`/api/public/apps` 为兼容别名，行为相同 |
| `GET` | `/manifest/{id}.plist` | 安装清单（远程模式下 302 跳转到 `/genPlist`） |
| `GET` | `/ipa/{id}.ipa` | 签名后的 IPA |
| `GET` | `/assets/{name}` | 图标文件 |
| `GET` | `/healthz` | 健康检查，返回 `status` 与 `plist_mode` |
| `GET` | `/` `/install` `/static/*` | 控制台、独立安装页、静态资源 |

---

## 数据存储与安全

- **必须备份 `data/master.key`**：丢失后所有已上传的证书都无法解密，只能重新上传。也可用 `IOS_SIGN_MASTER_KEY` 由外部密钥管理注入。
- 用户密码使用 PBKDF2-HMAC-SHA256 加盐哈希，不存明文；证书三件套使用 AES-256-GCM 加密；zsign 输出日志会脱敏，不会打印证书密码。
- 数据目录权限为 `0700`。`data/`、`config.json`、`bin/zsign*`、编译产物、`.workbuddy-ai/` 都应排除在版本控制之外。
- 安装数据按账号隔离：普通用户只能看到自己的产物；管理员可以看到全部。
- 反向代理场景下信任 `X-Forwarded-Host` / `X-Forwarded-Proto`，请确保代理会覆盖客户端伪造的这两个头。
- 本服务面向**自托管可信环境**，未实现速率限制、CSRF token、证书有效期/UDID 校验，请勿直接暴露在公网无认证环境。

---

## 多平台编译

纯 Go 标准库且 `CGO_ENABLED=0`，可以直接交叉编译：

```bash
# Windows amd64
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o dist/ios-auto-sign-windows-amd64.exe ./cmd/server

# Linux amd64
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o dist/ios-auto-sign-linux-amd64 ./cmd/server

# Linux arm64
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o dist/ios-auto-sign-linux-arm64 ./cmd/server
```

前端是 `go:embed` 内嵌的，**任何前端改动都必须重新编译二进制才会生效**。

开发验证：

```bash
go vet ./...
go test ./...
node --check internal/web/static/app.js
```

---

## 部署建议

### systemd（Linux）

```ini
[Unit]
Description=ios-auto-sign
After=network.target

[Service]
Type=simple
WorkingDirectory=/opt/ios-auto-sign
ExecStart=/opt/ios-auto-sign/ios-auto-sign-linux-amd64 -config /opt/ios-auto-sign/config.json
Environment=IOS_SIGN_MASTER_KEY=...
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

### 反向代理（Caddy 示例）

```
sign.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

Caddy 默认会传递 `X-Forwarded-*`，服务据此生成正确的 HTTPS 安装地址。此时建议把 `public_base_url` 设为 `https://sign.example.com`，让会话 Cookie 带上 `Secure`。

### Windows

把 `ios-auto-sign.exe`、`config.json`、`bin/zsign.exe` 放在同一目录，从该目录启动。可用计划任务、NSSM 或 `sc.exe` 注册为服务。注意 `zsign_path` 在 Windows 下应为 `bin/zsign.exe`。

---

## 常见问题

**Q：注册 / 登录点了没反应？**
确认访问的是本服务的端口（不是本机其他 Web 服务），并查看浏览器控制台是否有脚本错误。登录与注册共用一个表单，通过顶部分段控件切换。

**Q：签名任务失败，日志提示 zsign 不存在？**
把对应平台的 zsign 放到 `bin/`，或把 `zsign_path` 指向绝对路径。Windows 下文件名是 `zsign.exe`。

**Q：为什么 `fetchurl` 里的地址不是我配置的域名？**
安装地址改为按当前浏览器请求实时生成，`public_base_url` / `ipa_base_url` 不再参与链接拼装。用哪个地址访问，就生成哪个地址；走代理时请传递 `X-Forwarded-Host` / `X-Forwarded-Proto`。

**Q：远程 plist 模式报 `plistserver returned HTTP 404`？**
通常是 `plistserver_url` 已写成完整 `/genPlist` 又被重复拼接。当前版本会识别完整 endpoint，不会重复追加；请确认地址本身可访问。

**Q：点击安装没反应 / 提示无法安装？**
iOS OTA 要求 manifest、IPA、图标全部是受信任的 HTTPS。请在 iOS Safari 中打开，并配置域名与证书。桌面浏览器不会触发安装。

**Q：一添加订阅源就收到一堆 Bark 通知？**
首次导入是静默的，不会推送历史版本；只有之后新增的版本才会通知。如果仍收到，检查是否删除后重新添加了同一个源。

**Q：二维码扫码后没有直接弹出安装？**
`itms-services` 链接在 iOS 相机扫码后通常需要在 Safari 提示中再确认一次。若要「扫码即弹安装」，需要另做一个免登录的 HTTPS OTA 落地页。

**Q：证书密码会不会出现在日志里？**
不会，zsign 输出与错误都会经过 `redact()` 脱敏。

---

## 已知限制

- 独立 `/install` 页因账号隔离需要登录；没有免登录的公开安装页。
- 存储为单文件 JSON，适合单机小规模使用；多实例 / 高并发场景建议替换为 SQLite（已预留 `Update` / `View` 接口缝）。
- 未实现证书有效期、UDID 与描述文件匹配校验，也未校验 `codesign` 结果。
- 未实现速率限制、CSRF token、双因素认证；公网部署请自行加网关策略。
- 首次导入订阅源只建立目录，不会自动为历史版本创建签名任务。
- 项目尚未附带 `LICENSE` 文件，公开发布前建议补充许可证声明。

---

## 第三方组件

| 组件 | 用途 | 说明 |
| --- | --- | --- |
| [zsign](https://github.com/zhlynn/zsign) | IPA 重签名 | 外部可执行文件，需自行下载放置；本项目不分发其二进制，请遵守其许可证 |
| [qrcode-generator](https://github.com/kazuhikoarase/qrcode-generator) | 浏览器本地生成二维码 | `internal/web/static/qrcode.js`，MIT 许可，已本地化以便离线使用 |
| [braless/soft_collections](https://github.com/braless/soft_collections) | 默认订阅源示例 | `all-apps.json`，AltStore 格式 |
| `api.palera.in/genPlist` | 远程 plist 生成示例 | 仅作为 `plistserver` 模式的可选外部服务，本项目不依赖其可用性 |
