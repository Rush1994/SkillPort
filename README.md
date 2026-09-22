# SkillPort

在本地目录与 OCI 仓库之间同步可编辑的 Skills，当前支持 Harbor。

`skillport` 是原生 Go CLI，支持 Windows、HTTP/HTTPS 自动识别和 Docker 登录凭据复用；不执行 Skill 内容，不依赖 WSL 或 Docker Engine。

> 当前为开发预览：单 Skill 往返同步已可用，批量、文本 diff 和完整恢复机制尚未完成。参见[开发路线](docs/roadmap.md)。

## 构建

需要 Go 1.25.0 或更高版本。当前从源码构建，不假定已有公开发行包。

```powershell
go mod download
go build -trimpath -o dist/skillport.exe ./cmd/skillport
.\dist\skillport.exe version
```

Linux/macOS：

```sh
go build -trimpath -o dist/skillport ./cmd/skillport
./dist/skillport version
```

构建后的程序无需 Go 即可运行。Docker 配置指定的凭据助手仍须可用。

## 快速开始

将示例地址替换为自己的仓库，并先在 Harbor 中创建 `skills` 项目。仓库名须与 `SKILL.md` 中的 name 一致。

```powershell
# 使用随源码提供的无秘密样本
.\dist\skillport.exe validate --dir testdata/skills/valid/hello-skill

# 直接使用完整地址，不需要创建 team 别名或传 --plain-http
.\dist\skillport.exe push registry.example.com:5000/skills/hello-skill:1.0.0 --dir testdata/skills/valid/hello-skill
.\dist\skillport.exe pull registry.example.com:5000/skills/hello-skill:1.0.0 --target '.\my skills'

# 用编辑器修改下载目录，然后查看变化并发布新标签
.\dist\skillport.exe status --dir '.\my skills\hello-skill'
.\dist\skillport.exe push --dir '.\my skills\hello-skill' --tag review-2
```

已有的 Docker 登录凭据会被复用。首次上传必须指定标签，不默认使用 latest；已有标签内容不同则停止。下载得到普通文件目录，未跟踪的非空目标不会被覆盖。

## 连接行为

无显式协议或匹配的 CLI 配置时，先无凭据探测 HTTPS，再按需尝试 HTTP，并识别本地 Docker `insecure-registries`。也可以直接写 `http://registry.example.com:5000/skills/hello-skill:1.0.0` 固定协议。

**自动模式的 HTTPS 默认接受不受信任证书，HTTP 为明文。** 需要证书校验时显式使用 `--insecure=false` 或 `--ca-file`；保存的连接策略仍优先。认证后的失败不会再切换协议。完整规则见[使用指南](docs/usage.md)。

## 可用命令

| 命令 | 功能 |
|---|---|
| `registry add/list/use/remove` | 管理本地仓库别名与连接配置 |
| `validate` | 离线检查元数据、路径、敏感内容及限额 |
| `push` / `pull` | 单 Skill 上传、下载、来源补全与 dry-run |
| `status` | 离线比较本地与同步基线 |
| `tags` / `inspect` | 标签名称与制品元数据查询 |
| `doctor` | 检查连接和凭据来源，不写远端 |
| `version` | 查看构建与平台信息 |

支持 `--format json` 和非交互使用。更多参数执行 `skillport <command> --help`。

## 文档与贡献

- [使用指南](docs/usage.md)：配置、认证、协议、校验、冲突和退出码。
- [架构](docs/architecture.md)：模块边界和数据流程。
- [制品格式](docs/artifact-format.md)：OCI Skill v1 契约。
- [开发路线](docs/roadmap.md)：当前限制和稳定版本门槛。
- [贡献指南](CONTRIBUTING.md)：构建、测试和提交约定。
- [安全说明](SECURITY.md)：安全边界及报告方式。

测试使用临时目录和模拟服务，可直接运行 `go test ./...`。发布目标为 Windows amd64、Linux amd64/arm64 和 macOS arm64；原生运行结果与交叉编译结果分开记录，不将其视为完整兼容承诺。

当前 Go module 使用公开仓库地址 `github.com/Rush1994/SkillPort`。

## 许可证

项目自有代码和文档采用 [Apache License 2.0](LICENSE)。第三方依赖保留各自许可证和版权声明。
