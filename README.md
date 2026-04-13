# Interview-level Filesystem MCP Server (Go)

这个版本升级为“面试级 MCP Server（带 agent 能力）”，采用标准 MCP stdio framing（`Content-Length`），并提供可组合的文件操作 + 轻量 agent 编排能力。

## 核心能力

### 基础文件工具
- `list_dir`：列出目录
- `read_file`：读取文件
- `write_file`：写入文件
- `stat`：查看元数据
- `find_text`：递归搜索文本
- `apply_patch`：单点文本替换（防止 0 次/多次歧义替换）

### Agent 工具
- `agent_plan`：根据 `goal` 生成可执行 action plan
- `agent_execute`：按步骤执行 action plan（支持 `dry_run`）

## 安全设计

- 所有路径都限制在 `MCP_FS_ROOT` 根目录下。
- 阻止路径逃逸（如 `../`）。
- `agent_execute` 禁止递归调用 `agent_*` 工具，防止循环执行。

## 运行

```bash
go build -o mcp-filesystem-server
MCP_FS_ROOT=/path/to/sandbox ./mcp-filesystem-server
```

## MCP 通信示例（framed）

> 每条消息都必须带 `Content-Length` 头。

```text
Content-Length: 52

{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}
```
