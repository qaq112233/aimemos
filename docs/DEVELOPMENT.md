# 开发、验证与发布

## 本地构建

需要 Go 1.27.2 或更高版本。CI 从 `go.mod` 读取工具链版本，Docker 构建版本与其保持一致。静态检查使用 golangci-lint 2.15.0。

```sh
go test -race ./...
go vet ./...
golangci-lint run
sh scripts/build.sh
./build/memogram --version
```

普通开发构建从 Git 提交时间和提交号生成版本。发布构建显式指定：

```sh
MEMOGRAM_VERSION=v0.2.0 sh scripts/build.sh
./build/memogram --check-config notebook.example.yaml
```

`--version` 和 `--check-config` 不需要真实服务；正式启动必须通过环境配置和允许用户校验。

## 源码 Compose

准备 `.env`，AI 模式还需准备 `notebook.yaml`：

```sh
# AI 笔记模式
sh scripts/build.sh compose -f compose.notebook.yaml config --quiet
sh scripts/build.sh compose -f compose.notebook.yaml up -d --build

# 即时保存模式
sh scripts/build.sh compose up -d --build
```

构建脚本传入版本和提交号。持久卷保存绑定文件，AI 配置以只读方式挂载。环境修改后重新创建容器，YAML 修改后重启。`docker compose down` 保留卷，带 `-v` 则删除卷。

## 模块职责

| 目录 | 职责 |
| --- | --- |
| `cmd/memogram` | 读取配置、连接各模块、处理退出信号 |
| `internal/config` | 环境变量、允许用户、AI YAML 与参数校验 |
| `internal/bot` | Telegram 消息、附件、相册、命令和按钮 |
| `internal/workflow` | 按用户隔离的内存素材组、快照、草稿和保存状态 |
| `internal/ai` | 官方 Go SDK、Responses / Chat Completions、图片数据与参数映射 |
| `internal/memos`、`internal/memosapi` | Memos 客户端、API 版本识别与生成代码 |
| `internal/store` | 即时保存模式的用户令牌、空间设置持久化 |
| `internal/version` | 版本与提交号元数据 |

保留上游客户端、生成接口和成熟行为。变更访问规则时，同时验证消息、按钮、启动校验和两种模式。变更并发流程时，验证用户隔离、快照、过期按钮及重复确认。

## 测试层次

```sh
# Go 单元测试、模拟服务集成测试和竞态检查
go test -race ./...
go vet ./...
golangci-lint run

# 启动本地二进制，连接模拟 Telegram / AI / Memos
python3 scripts/test-notebook.py ./build/memogram

# 容器冒烟脚本自身的回归测试
python3 -m unittest discover -s scripts -p 'test_container_*.py' -v

# 构建并验证容器启动、非 root 用户和绑定持久化
sh scripts/build.sh docker -t memogram:test
python3 scripts/test-container.py memogram:test
```

模拟服务测试覆盖两种 AI 协议、多图与说明、参数覆盖、访问名单、不同用户的草稿、错误恢复和重复确认，不验证真实模型的理解质量。

真实 Memos 兼容性测试必须使用全新、可丢弃的实例，测试会创建用户和笔记：

```sh
docker run --rm -d --name memogram-memos-test \
  -e MEMOS_MODE=prod -p 127.0.0.1:5231:5230 neosmemo/memos:0.31.0
MEMOS_TEST_URL=http://127.0.0.1:5231 go test -race -run '^TestMemosIntegration$' -count=1 -timeout=3m -v ./internal/bot
docker rm -f memogram-memos-test
```

未设置 `MEMOS_TEST_URL` 时，该测试自动跳过。已有用户的实例会被拒绝。CI 对 `canary`、`0.30.0`、`0.31.0` 分别执行；附件上传后下载并逐字节比较。语音和视频仅验证传输，不验证播放。

## 发布 v0.2.0

发布标签采用 `v主版本.次版本.修订号`；候选版本使用 `-rc.1` 等后缀，并标记为预发布。程序打印不带 `v` 的版本及源码提交号。保留对历史上游日期版本的解析。

1. 验证代码、配置示例和中文文档，提交并推送到本仓库 `main`。
2. 确认 `v0.2.0` 标签指向本次提交。继承的本地上游标签可能指向旧代码，不能据此发布。
3. 在本仓库创建并发布 GitHub Release，填写中文标题、更新内容和升级事项。
4. 等待 `CI` 和 `Build and publish` 完成，检查压缩包与 GHCR 的架构、版本和源码提交号。

`Build and publish` 只在发布 Release 或手动运行时执行。发布稳定版会上传压缩包、构建 `linux/amd64` 与 `linux/arm64` 镜像，并更新 `v0.2.0`、`0.2.0`、`latest` 标签。预发布和手动构建不会更新 `latest`。手动构建默认版本为 `v0.2.0`，会推送镜像，但不会创建 Release 或远程 Git 标签。

流程使用仓库 `GITHUB_TOKEN` 上传附件和推送 GHCR，无需另外保存注册表密钥。镜像如需匿名拉取，GHCR 软件包需设为公开。重复使用版本会替换对应镜像标签，日后应使用新版本。

本地验证发布打包需安装 GoReleaser 2.18.3：

```sh
goreleaser check
python3 scripts/test-release.py
```

脚本在临时克隆中打包稳定版、候选版和历史日期版本，检查本机压缩包中的二进制版本；不会发布或修改原仓库标签。跨系统编译会占用较多资源，建议交给 GitHub Actions。本地脚本按单个目标顺序打包，默认 `GOMAXPROCS=2`、`GOFLAGS=-p=2`；不要在资源受限的 WSL 中同时运行多个构建和静态检查。

直接构建 Docker 时必须提供 `VERSION=0.2.0` 和 `COMMIT=<源码提交号>`，因为 Docker 上下文不包含 `.git`。

## 修改接口快照

生成文件不能手动编辑。使用 `go generate ./...` 更新，并检查生成差异。固定的接口来源和兼容性要求见 [Memos API 快照说明](../openapi/README.md)。
