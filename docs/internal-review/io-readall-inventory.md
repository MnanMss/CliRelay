# io.ReadAll Inventory

更新时间：2026-04-14 09:46:33 +0800

## 分类结论

当前非测试代码中的 `io.ReadAll` 主要分为：

1. 请求体读取
2. 上游 HTTP 响应读取
3. 压缩内容解码读取
4. 对象存储 / 文件读取

## 高优先级优先收敛区

- `CliRelay/internal/runtime/executor/*`
  大量读取上游响应体，是 provider-specific 限制的主要落点。
- `CliRelay/internal/api/handlers/management/auth_files.go`
  管理接口与 OAuth 辅助路径混在一起。
- `CliRelay/internal/api/handlers/management/api_tools.go`
  管理端代理调用与 OAuth token 刷新路径。
- `CliRelay/internal/store/objectstore.go`
  对象存储读取路径。
- `CliRelay/internal/logging/request_logger.go`
  压缩内容解码与日志内容回读路径。

## 已完成底座

- 请求体读取已优先收敛到 `bodyutil.ReadRequestBody` / `LimitBodyMiddleware`。
- OpenAI / Gemini / Claude SDK API 入口已经不再直接 `GetRawData()`，统一经 `bodyutil.ReadRequestBody` 走默认大小限制与一致错误响应。
- 管理接口下仍有 `GetRawData()` 的配置编辑路径，但它们已经统一挂在 `LimitBodyMiddleware(bodyutil.ManagementBodyLimit)` 之后，不再绕过请求体大小限制。
- auth 文件 raw 上传和 Vertex multipart 上传已有服务端大小限制。

## 下一步

- 为 executor 层按 provider 设置响应体读取上限。
- 为对象存储和压缩内容解码设置单独上限。
- 避免把所有 `io.ReadAll` 统一粗暴替换成同一个上限，按来源分类治理。
