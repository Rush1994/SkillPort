# 架构

SkillPort 将普通本地目录与 OCI Registry 中的 Skill 制品同步。核心为原生 Go 程序；项目面向 Harbor，不包含服务器、数据库、Web 市场或 Agent 运行时。

## 模块

| 路径 | 职责 |
|---|---|
| `cmd/skillport` | 入口、构建版本、取消信号 |
| `internal/cli` | Cobra 命令、引用解析、选项优先级、输出 |
| `internal/config` | CLI JSON 配置及 endpoint 校验 |
| `internal/auth` | Docker 配置、stdin/环境凭据、只读 helper 调用 |
| `internal/registry` | 无凭据协议探测、ORAS 客户端、TLS 和错误分类 |
| `internal/skill` | YAML 元数据、扫描、哈希、确定性归档和安全解包 |
| `internal/sync` | 基线、目录锁、冲突、发布及本地提交 |
| `internal/errs` | 可公开输出的错误类型 |
| `internal/testregistry` | 内存 Registry 测试替身，不是 Harbor 服务 |

## 依赖选择

标准库负责文件、JSON、哈希、tar/gzip、HTTP 和 TLS；Cobra 负责命令行，ORAS Go 负责 OCI 传输和凭据助手协议。使用 yaml.v3 节点 API 检查 frontmatter，patternmatcher 处理忽略规则，flock 保护目录并发。

依赖版本以 [go.mod](../go.mod) 和 [go.sum](../go.sum) 为准。核心传输不调用 docker/oras CLI；只有 Docker 配置明确指定的 credential helper 是外部认证进程。配置使用 JSON，Skill frontmatter 使用 YAML。

## 上传

1. 解析目标和来源记录，确定连接策略；自动模式先匿名探测，再读取凭据。
2. 锁定目录，扫描到有界内存快照，校验名称、内容和大小。
3. 从已校验字节生成 config、gzip 内容层和 manifest。
4. 检查目标标签；上传 blob 后，在提交 manifest 前再次检查标签。
5. manifest 提交成功后保存本地基线。基线失败仍返回已发布 digest，不能声称未上传。

快照使校验与上传使用相同字节；快照后的本地编辑留给后续 status 识别。标签复查不提供原子 compare-and-swap。

## 下载

解析 tag/digest 后按具体 manifest 下载，逐层核对实际大小和 SHA-256。内容解包到目标旁新建的私有暂存目录，验证元数据和路径后，在其中写入来源基线。

目标非空但未跟踪、不同源或存在冲突时停止。干净目标先整体改名为保留备份，再提交暂存目录；提交失败时尝试恢复旧目录。此机制不等于断电安全事务，完整恢复日志仍在[开发路线](roadmap.md)中。

## 数据边界

- Skill 文档、脚本、归档、远端元数据和同步记录始终是数据，不执行其中指令。
- 身份凭据不进入日志、制品或 `.skillport`。不修改 Docker 认证文件、不调用 logout。
- 路径、条目数和字节限额由业务代码验证，不能只相信 tar 头或远端 descriptor。
- 同步记录使用 schemaVersion 1，基线文件字节作为 JSON base64 保存；读取时验证路径和哈希。
- Windows 单独检查 reparse point 和硬链接；不能把 Linux 重命名行为当作 Windows 已验证行为。
- 自动识别仅用于认证前选择协议，固定协议和认证后的请求不做失败降级；跨主机 token/重定向拒绝。

## 格式与兼容

公开制品契约见 [Skill OCI v1](artifact-format.md)。当前不承诺与其他 Skill 工具的私有制品格式兼容。CLI 来源记录仅用于目录同步，不是项目依赖锁文件。
