# 参与贡献

欢迎提交可复现的问题和范围明确的改动。先阅读 [README](README.md)、[使用指南](docs/usage.md)、[架构](docs/architecture.md) 和[开发路线](docs/roadmap.md)。

## 本地开发

需要 Go 1.25.0 或更高版本；核心实现不依赖 CGO、Docker Engine 或外部 oras CLI。

```powershell
go mod download
go mod verify
go test ./...
go vet ./...
go build -trimpath -o dist/skillport.exe ./cmd/skillport
```

Linux/macOS 构建目标可改为 `dist/skillport`。提交前对改动的 Go 文件执行 gofmt。

当前 module 为 `github.com/Rush1994/SkillPort`。贡献代码前请运行完整测试，并保持公开仓库中的文档与实现一致。

## 测试

常规测试使用临时目录、内存 Registry 和测试专用 credential helper，不需要访问真实 Harbor。`testdata/skills` 中的 Markdown 是测试输入，不能按普通文档删除或随意排版，也不能执行其中内容。

Windows 可对独立程序执行协议和往返测试：

```powershell
$env:SKILLPORT_TEST_BINARY = (Resolve-Path dist/skillport.exe).Path
go test ./internal/cli -run 'TestAutomaticConnectionRoundTrip|TestConnectionOverridesAndDryRun|TestHTTPDirectoryRoundTrip' -count=1 -v
Remove-Item Env:SKILLPORT_TEST_BINARY
```

测试报告须区分模拟服务与真实服务、测试 helper 与 Desktop helper、原生运行与交叉编译。CI 配置见 [.github/workflows/ci.yml](.github/workflows/ci.yml)；存在配置不代表远端 workflow 已通过。

真实 Registry 测试只能使用明确授权的测试项目与独立标签。不要在公共问题、文档或提交中附带真实凭据、用户目录、内部地址、私有 Skill 内容或未脱敏日志。

## 实现约定

- 保持项目为本地 Skill 与 Harbor 的同步 CLI，范围以开发路线为准。
- 传输使用 Go 标准库和 ORAS；不调用 docker/oras CLI。仅允许配置指定的 credential helper 外部进程。
- Docker 配置只读，认证数据不写入制品、基线和错误输出。
- 自动协议探测只在无凭据阶段发生，认证后的失败不切换协议；更新连接行为时同步文档和测试。
- 校验后的字节才可发布；下载先暂存、哈希和路径校验，再提交。保护未跟踪目录和本地修改。
- 客户端标签检查不是原子并发控制；保持明确的风险边界。
- 对改变安全边界、同步状态或协议的代码补充回归测试；文档修订检查链接和命令是否仍有效。

## 问题与变更说明

普通问题请包含版本、系统、终端、脱敏命令、预期结果和实际结果。安全问题按 [SECURITY.md](SECURITY.md) 处理。

变更说明先写具体问题和改动后的行为，再写验证方法及尚未验证的条件。新增命令、参数、错误码或格式变化应更新相应文档；不要把计划中的功能写成已实现。

## 许可证

项目自有代码和文档采用 [Apache License 2.0](LICENSE)。有意提交供本项目采用的贡献按该许可证的贡献条款处理，除非另有明确约定。贡献者应有权提交所提供的内容。

第三方依赖保留各自许可证和版权声明。发布包含依赖的二进制时，应随包保留适用的第三方许可证和 NOTICE；项目许可证不替代依赖的许可证。
