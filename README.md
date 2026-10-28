# AIMemos / Memogram

通过 Telegram 收集文字、网址、图片和文件，交给 AI 整理成 Markdown 草稿，确认后保存到 Memos。当前版本 **v0.3.2**，支持多图理解、草稿修改、多供应商、两种 AI 协议和 Memos 的 Cloudflare Access 认证。

项目基于 [usememos/telegram-integration](https://github.com/usememos/telegram-integration)，保留上游历史与即时保存模式；程序和发布文件仍使用 `memogram` 名称。

## 两种使用方式

| 模式 | 配置 | 保存行为 | Memos 账号 |
| --- | --- | --- | --- |
| AI 笔记模式 | 设置 `NOTEBOOK_CONFIG=notebook.yaml` | 收集 → 生成草稿 → 修改 → 确认保存 | 使用环境中的 `MEMOS_TOKEN`，获准用户共用此账号 |
| 即时保存模式 | `NOTEBOOK_CONFIG` 留空 | 收到文字或附件后直接保存 | 每个用户通过 `/start <令牌>` 绑定自己的账号 |

AI 笔记模式只接受私聊，素材、草稿、模型选择和确认按钮按 Telegram 用户隔离。内存中的未保存内容会随进程重启丢失。网址只保留链接和附带文字，不抓取网页正文。

- [AI 笔记模式与图片理解](README_NOTEBOOK.md)
- [网页配置与独立密码](docs/WEB_SETTINGS.md)
- [更新日志与升级事项](CHANGELOG.md)
- [开发、测试与发布](docs/DEVELOPMENT.md)
- [功能约定](docs/REQUIREMENTS.md)

## 先配置允许用户

**两种模式都必须配置允许用户，否则程序拒绝启动。** 从下面选择一种，在 `.env` 填写：

```dotenv
# 按 Telegram 用户名允许，可带 @，不区分大小写。
ALLOWED_USERNAMES=alice,bob
```

```dotenv
# 按 Telegram 数字用户 ID 允许，账号不需要设置用户名。
ALLOWED_USER_IDS=123456789,987654321
```

原有 `TELEGRAM_USER_ID=123456789` 继续有效，并与 ID 名单合并。用户名与 ID 同时配置时，任意一项匹配即可使用。数字 ID 指发送人的用户 ID，不是群聊 ID、手机号或 Bot Token。

如果确实需要允许所有人，必须**手动**将其中一个字段单独填写为小写 `none`，另外两个字段留空：

```dotenv
ALLOWED_USERNAMES=none
ALLOWED_USER_IDS=
TELEGRAM_USER_ID=
```

所有示例默认留空，不会默认设置 `none`。全部留空、无效 ID、仅有逗号，或把 `none` 与其他名单混填，都会拒绝启动。检查同时作用于消息和按钮。AI 笔记模式即使开放所有人，也只接受各用户自己的私聊；所有获准用户的笔记仍保存到配置的同一个 Memos 账号。

## 准备服务

需要已经运行的 Memos、Telegram Bot，以及 AI 笔记模式使用的模型服务。项目不包含 Memos 服务。

当前代码及 CI 兼容范围为 Memos **0.30.x、0.31.x 和 canary 开发版**。空间功能需要 Memos 0.31 或以上；具体业务兼容性以 CI 中对应版本的结果为准。

1. 创建 Telegram Bot，取得 Bot Token。
2. 在 Memos 设置中创建个人访问令牌。
3. AI 笔记模式准备兼容 Responses 或 Chat Completions 的供应商地址、密钥和模型 ID。
4. 下载本仓库，或克隆到服务器：

```sh
git clone https://github.com/qaq112233/aimemos.git
cd aimemos
cp .env.notebook.example .env
cp notebook.example.yaml notebook.yaml
chmod 600 .env
```

编辑 `.env`，填写 `SERVER_ADDR`、`BOT_TOKEN`、`MEMOS_TOKEN`、允许用户，以及供应商密钥。编辑 `notebook.yaml`，填写供应商地址与实际模型 ID，选择协议和读图能力。

即时保存模式使用 `cp .env.example .env`，无需 AI 配置和 `MEMOS_TOKEN`；启动后在私聊发送 `/start <Memos 个人访问令牌>` 绑定账号。

## 使用发布的 Docker 镜像

```sh
docker pull ghcr.io/qaq112233/aimemos:v0.3.2
docker volume create memogram-data
docker run -d --name memogram --restart unless-stopped \
  --env-file .env \
  --add-host host.docker.internal:host-gateway \
  -e DATA=/data/data.txt \
  -e WEB_ADDR=0.0.0.0:8080 \
  -p 127.0.0.1:8080:8080 \
  -v memogram-data:/data \
  -v "$PWD/notebook.yaml:/app/notebook.yaml:ro" \
  ghcr.io/qaq112233/aimemos:v0.3.2
docker logs -f memogram
```

即时保存模式省略 `notebook.yaml` 挂载。镜像支持 `linux/amd64` 和 `linux/arm64`，以 UID/GID `65532:65532` 运行。`/data` 必须可写并持久化，绑定文件包含 Memos 访问令牌，应限制访问。

容器内的 `localhost` 指容器本身。连接宿主机上的 Memos、AI 或代理时，使用 `host.docker.internal` 或实际可访问地址。修改 `.env` 后需重新创建容器；只修改 `notebook.yaml` 可重启容器。

更新镜像时保留 `.env`、`notebook.yaml` 和数据卷：

```sh
docker pull ghcr.io/qaq112233/aimemos:v0.3.2
docker stop memogram
docker rm memogram
# 使用上面的 docker run 命令重新创建容器，复用原有数据卷。
```

源码构建和现有 Compose 文件的用法见[开发文档](docs/DEVELOPMENT.md)。

## 使用发布的二进制

本项目仅支持 **Linux amd64、arm64**，后续发布只提供这两种架构的压缩包和 Docker 镜像。从本仓库 [Releases](https://github.com/qaq112233/aimemos/releases) 下载对应 Linux 架构的压缩包，解压后按上述步骤准备 `.env` 和 `notebook.yaml`：

```sh
./memogram --version
./memogram --check-config notebook.yaml
./memogram
```

`--check-config` 只检查 YAML 结构，不验证运行时凭据、允许用户或服务连接。正式启动才会验证完整环境配置。程序从工作目录加载 `.env`，进程环境中的同名变量优先。

## 日常操作

AI 笔记模式中，连续发送的素材留在同一组；四秒状态提示不会自动生成或保存。

| 操作 | 作用 |
| --- | --- |
| `1` / 原样保存 | 合并原文和来源后直接保存，不调用 AI |
| `2` / 简单整理 | 只整理文字与图片说明，不发送图片像素 |
| `3` / 教程整理 | 结合图片组织前提、步骤与注意事项 |
| `4 本次提示词` | 按本次要求整理，默认读图 |
| `5` / 图片理解 | 提取图片文字、数据和可见细节，生成笔记草稿 |
| 普通文字 | 有草稿时作为修改要求；收集状态作为素材 |
| 确认保存 / 补充素材 / 放弃本组 | 按钮操作当前用户的素材组和草稿 |
| `/status`、`/model`、`/retry` | 查看状态、选择模型、重试整理 |
| `/settings` | 打开配置好的网页地址，使用独立密码登录 |

即时保存模式支持文字、图片、文件、语音、视频、相册，另有 `/search <关键词>` 和 `/space`。保存后的按钮可切换可见性、置顶或所属空间。进入空间的笔记对该空间成员可见。

## 环境变量

| 变量 | 用途 |
| --- | --- |
| `SERVER_ADDR` | 必填，Memos 地址，例如 `https://memos.example.com` |
| `MEMOS_CF_ACCESS_CLIENT_ID`、`MEMOS_CF_ACCESS_CLIENT_SECRET` | 可选，Memos 的 Cloudflare Access Service Token，必须成对填写；仅发送到 Memos，不替代 Memos 自身令牌 |
| `BOT_TOKEN` | 必填，Telegram Bot Token |
| `ALLOWED_USERNAMES` | 允许的用户名列表，与下面的 ID 名单至少填写一项 |
| `ALLOWED_USER_IDS` | 允许的正整数用户 ID 列表，逗号分隔 |
| `TELEGRAM_USER_ID` | 兼容旧配置的 ID 字段，与 `ALLOWED_USER_IDS` 合并 |
| `NOTEBOOK_CONFIG` | AI 笔记模式 YAML 路径，留空启用即时保存模式 |
| `WEB_ADDR` | 笔记模式的网页监听地址，默认 `127.0.0.1:8080`；`off` 关闭网页入口 |
| `WEB_URL` | 网页配置的公网 HTTPS 地址，用于机器人 `/settings` 按钮 |
| `MEMOS_TOKEN` | AI 笔记模式必填，Memos 个人访问令牌 |
| `CLIPROXY_API_KEY`、`COMPATIBLE_API_KEY` | 示例供应商密钥；实际名称由 `key_env` 决定 |
| `PROXY_URL` | Telegram 和未单独配置的 AI 供应商共用的网络代理 |
| `TELEGRAM_PROXY_URL` | Telegram API 和文件下载的专用网络代理 |
| `BOT_PROXY_ADDR` | Telegram Bot API 服务地址，通常留空，不是 HTTP/SOCKS5 代理 |
| `DATA` | 绑定文件路径；二进制默认 `data.txt`，容器默认 `/data/data.txt` |

Memos 受 Cloudflare Access 保护时，在对应应用中配置 **Service Auth** 策略，Include 选择使用的 Service Token，并将其 ID 和 Secret 填入上面的 `MEMOS_CF_ACCESS_*` 变量。程序会同时携带 Cloudflare 的两个认证请求头和 Memos 自身的令牌；Telegram 和 AI 请求不携带这些 Cloudflare 凭据。

代理支持 HTTP、HTTPS、SOCKS5 和 `socks5h`，可包含用户名和密码。`direct` 强制直连；专用项留空时继承统一代理。所有层都未指定时，沿用 `HTTP_PROXY` / `HTTPS_PROXY` / `NO_PROXY`。供应商专用代理见[笔记模式配置](README_NOTEBOOK.md#网络代理)。

## 升级到 v0.3.0

1. 填写允许用户；旧版依赖空名单开放访问的部署必须调整，只有显式 `none` 才开放。
2. 原有 `TELEGRAM_USER_ID` 仍可继续使用；仅按用户名授权也能运行 AI 笔记模式。
3. 已有 YAML 如需显示新入口 `5`，添加示例中的 `profiles.vision` 与 `entries."5"`，保留自己的供应商、密钥引用和提示词。
4. 保留绑定文件与数据卷。重启前先处理内存中的素材、草稿和待补传附件。

## 验证与边界

全量 Go 测试包含竞态检查，模拟服务测试覆盖访问规则、用户会话隔离、两种 AI 协议、多图、草稿修改和确认保存。发布流程另外检查构建产物，并构建多架构镜像。真实模型的识图质量、服务商扩展参数和实际部署连通性需要使用对应服务验证。

同一进程内，已知成功的保存不会因重复确认再次创建正文。Memos 写入成功但返回前断网时，结果可能不确定，重试前请检查笔记；跨重启不承诺去重。图片每次下载最多 20 MiB，默认每次生成最多读取八张。
