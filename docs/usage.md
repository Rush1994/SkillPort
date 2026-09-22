# 使用指南

本文描述当前已实现的行为。未实现功能见[开发路线](roadmap.md)。示例中的 `registry.example.com:5000` 应替换为实际地址，`skills` 项目须事先存在。

## Skill 目录

根目录必须有大小写准确的 `SKILL.md`：

```markdown
---
name: hello-skill
description: A small example Skill.
version: "1.0.0"
---

# Hello Skill

Describe the Skill here.
```

文件须为 UTF-8，支持 BOM、CRLF，正文非空。`name` 为 1–64 位小写 ASCII 字母、数字或单连字符，不能以连字符开头或结尾；`description` 为 1–1024 个 Unicode 码点。`version` 是可选字符串；使用 SemVer 形式标签时，存在的 version 必须与标签一致。

YAML 扩展字段保留在原文件中；当前拒绝重复键、自定义标签、anchor 和 alias。最多 20 层嵌套、64 KiB frontmatter。

## 上传与下载

```powershell
skillport validate --dir .\hello-skill
skillport push registry.example.com:5000/skills/hello-skill:1.0.0 --dir .\hello-skill --dry-run
skillport push registry.example.com:5000/skills/hello-skill:1.0.0 --dir .\hello-skill
skillport pull registry.example.com:5000/skills/hello-skill:1.0.0 --target '.\my skills'
skillport status --dir '.\my skills\hello-skill'

# 编辑下载目录后，用新标签发布，复用来源仓库
skillport push --dir '.\my skills\hello-skill' --tag review-2
skillport pull --dir '.\my skills\hello-skill'
```

以上命令假设已将可执行文件目录加入 PATH；源码构建后可用 `./dist/skillport.exe`（Windows）或 `./dist/skillport`（Linux/macOS）替代 `skillport`。

仓库末级名称必须与元数据 name 一致。首次上传须指定标签，不默认使用 latest。标签可写在引用里，也可使用 `--tag`；两者冲突时报错。完整引用支持 `host/project/name:tag`、`host/project/name@sha256:...` 及带 `http://`、`https://` 的形式。URL 不能含用户名、密码、查询参数或 fragment。

按 digest 下载后重新上传须显式指定 `--tag`。`push --dry-run` 只做本地预检，不上传、不写基线、不读取凭据、不探测网络；自动连接显示 `pending=true`。`pull --dry-run` 会读取远端并使用临时暂存目录，但不提交到目标 Skill 目录。

## 本地状态与冲突

`.skillport/state.json` 保存来源、manifest digest、同步时间、文件清单和基线字节，永不上传。下载目录可由普通编辑器修改，不执行脚本或钩子。

| 情况 | 当前行为 |
|---|---|
| 新标签不存在 | 上传并记录基线 |
| 标签已指向相同制品 | 返回 unchanged |
| 标签已存在且内容不同 | 停止，要求新标签 |
| 原跟踪标签被删除 | 停止，要求新标签 |
| 下载目标非空、未跟踪或属于其他来源 | 停止 |
| 本地有修改，远端仍等于基线 | 保留本地修改，不写内容 |
| 本地有修改，远端已变化 | 停止 |
| 本地干净，远端已变化 | 暂存校验后更新，保留旧目录备份 |

`status` 离线报告新增、修改、删除、未变化；新增的被排除文件也计为本地变化。备份位于目标目录旁的 `.skillport-backup-*` 路径，命令返回其绝对路径，不自动删除。当前没有 `--overwrite-local`、`push --force` 或自动合并。

客户端提交前复查标签不等于原子并发控制。需要严格避免并发覆盖时，应使用唯一标签及 Harbor 标签不可变规则。当前只具备进程内失败回滚，强杀和断电恢复尚未完整实现。

## 仓库别名（可选）

```powershell
skillport registry add team --endpoint http://registry.example.com:5000 --project skills
skillport registry use team
skillport registry list
skillport push --dir .\hello-skill --tag 1.0.0
skillport pull hello-skill:1.0.0
skillport registry remove team
```

`team` 只是本地别名，不是上传前提。默认配置文件位于 Go `os.UserConfigDir()` 下的 `skillport/config.json`，Windows 通常为 `%APPDATA%\skillport\config.json`。可用 `--config` 或 `SKILLPORT_CONFIG` 替换。配置只存 endpoint、project、CA 和 TLS 选项，不存密码。

## 协议和证书

没有显式协议或匹配的 CLI 仓库配置时，先无凭据探测 HTTPS `/v2/`，无法建立连接时再探测 HTTP。探测不跟随重定向或认证 challenge；选定协议后才读取凭据。自动模式接受不受信任的 HTTPS 证书，HTTP 使用明文连接。

显式 URL 或保存的 endpoint 固定协议；认证失败后不会换协议。无匹配配置的直接 `https://` 引用默认不校验证书，已有配置的 TLS 策略仍适用。

```powershell
# 固定 HTTP
skillport doctor http://registry.example.com:5000

# 固定 HTTPS 并校验证书
skillport doctor https://registry.example.com --insecure=false

# 企业 CA，默认启用校验
skillport doctor https://registry.example.com --ca-file .\company-ca.pem
```

`--plain-http` 固定 HTTP，`--plain-http=false` 固定 HTTPS；HTTP 与显式 TLS 选项互斥。命令行优先于环境，再优先于已保存配置；最后使用 Docker 文件策略和自动识别。跨主机 token realm/重定向当前保守拒绝。

自动模式读取第一个存在的本地 Docker daemon 配置，不合并多个 Engine 配置：

| 平台 | 按顺序查找 |
|---|---|
| 全部 | 用户主目录下 `.docker/daemon.json` |
| Windows 后续 | `%ProgramData%/docker/config/daemon.json` |
| Linux 后续 | `$XDG_CONFIG_HOME/docker/daemon.json`，未设时为 `~/.config/docker/daemon.json`；然后 `/etc/docker/daemon.json` |

识别 `insecure-registries` 中精确 host:port 和 CIDR，在结果的 `connection` 中报告来源。没有匹配也会自动探测。可用 `--docker-daemon-config` 指定文件；不修改 Docker 配置，不查询或启动 Engine，也不声称反映远端 Docker context 的生效设置。

## 登录凭据

认证来源依次为：显式 stdin、完整环境凭据、Docker 配置、匿名。用户名与密码须整组来自同一来源，不完整的显式配置直接失败，不偷偷回退。

- `--username` 与 `--password-stdin` 配对；`--identity-token-stdin` 独立使用。
- 环境变量使用 `SKILLPORT_USERNAME` + `SKILLPORT_PASSWORD`，或独立的 `SKILLPORT_IDENTITY_TOKEN`。
- Docker 文件选择：`--registry-config` > `$DOCKER_CONFIG/config.json` > 用户 `.docker/config.json`。
- 文件内顺序：目标 `credHelpers` > 全局 `credsStore` > `auths`，按精确 host:port 匹配；目前不归一化旧式 URL auths 键。
- `--anonymous` 禁用凭据读取；助手错误或超时不会回退到其他账号。

不依赖 Docker Engine。Docker Desktop helper 仍可能依赖 Desktop 组件或系统钥匙串。`--registry-config` 是登录配置，`--docker-daemon-config` 是连接策略，两者用途不同。

| 环境变量 | 对应设置 |
|---|---|
| `SKILLPORT_CONFIG` | CLI 配置文件 |
| `SKILLPORT_REGISTRY` | 仓库别名 |
| `SKILLPORT_PLAIN_HTTP` | 固定协议，布尔值 |
| `SKILLPORT_INSECURE` | HTTPS 跳过证书校验，布尔值 |
| `SKILLPORT_CA_FILE` | CA 文件 |
| `SKILLPORT_DOCKER_DAEMON_CONFIG` | Docker daemon 配置文件 |

## 校验和排除

`validate`、上传及下载暂存使用同一套基本校验。默认排除 `.git`、`.skillport`、`.env`/`.env.*`、常见凭据目录、私钥和密钥库路径；`.env.example`、`.env.sample`、`.env.template` 仍需内容扫描。

`.skillignore` 使用 Dockerignore 风格规则；排除路径和原因出现在 findings。有限的内容规则检查私钥标记、令牌和秘密赋值，命中后阻止上传，不显示秘密原文。二进制、压缩内容和超过 5 MiB 的文本标为 unscanned；`--strict` 拒绝这些未扫描警告。检测不能保证发现全部秘密，当前没有精确例外参数。

| 限制 | 当前固定值 |
|---|---|
| 单文件 | 20 MiB |
| 包含文件的原始总大小 | 50 MiB |
| 压缩内容层 | 50 MiB |
| tar 解码读取上限 | 200 MiB；另受 50 MiB 文件总大小限制 |
| 扫描条目 / 归档文件条目 | 10,000 |
| 路径组件深度 | 32 |

1 MiB = 1,048,576 字节。拒绝链接、特殊文件、越界和 Windows 不兼容路径。下载内容必须完整通过检查，不能删除被排除文件后声称下载完整。

## 查询和输出

```powershell
skillport tags registry.example.com:5000/skills/hello-skill
skillport inspect registry.example.com:5000/skills/hello-skill:1.0.0 --format json
skillport doctor registry.example.com:5000 --timeout 30s
```

`tags` 当前列标签名；`inspect` 返回指定制品的元数据和 digest。`doctor` 只检查连接及凭据来源，不证明上传权限。

`--format json` 信封为 `schemaVersion`、`success`、`data`、`error`；普通结构结果使用缩进 JSON，帮助为文本。`--quiet` 保留结果；核心命令不交互索要密码，支持 `--non-interactive`。整体超时默认 5 分钟，可用 `--timeout` 调整。

| 退出码 | 含义 |
|---|---|
| 0 | 成功，包括 status 存在变化 |
| 1 | 本地提交等未分类错误 |
| 2 | 参数或配置错误 |
| 3 | 认证失败 |
| 4 | 权限不足 |
| 5 | 网络、协议或 TLS 错误 |
| 6 | 校验失败 |
| 7 | 同步冲突或锁冲突 |
| 8 | 引用不存在 |
| 9 | 凭据助手失败 |
