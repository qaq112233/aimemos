# Memos API 快照

接口快照来自 [usememos/dotcom/openapi](https://github.com/usememos/dotcom/tree/main/openapi)。

- `latest.yaml`：Memos `main` 的接口快照。
- `0-30.yaml`、`0-31.yaml`：本项目兼容矩阵中的稳定版本系列。
- `generate.sh`：固定来源提交和 oapi-codegen 版本；普通构建使用已提交的生成客户端，不在线抓取接口。

在仓库根目录运行 `go generate ./...`，更新快照并生成 `internal/memosapi/client.gen.go`。只生成本项目和兼容性测试实际使用的接口；不要手动修改生成代码。

调整兼容范围时，更新快照、固定来源提交及 CI 版本矩阵，分别使用全新容器执行真实 Memos 集成测试后再提交。

各版本共用最新快照生成的客户端。启动时通过只读实例信息接口选择 `/api` 或 `/api/v1`；不会为发现版本而重放写入请求。契约测试比较实际使用的路径与 JSON 结构，容器测试验证服务器行为。
