# Interview-level Filesystem MCP Server (Go, with Qwen 3.5 Agent)

这个项目已升级为：**内含文件系统 MCP Server 的 Agent**。除了基础文件工具外，还支持接入 **千问 3.5** 做自动决策执行。

## 核心能力

### 基础文件工具
- `list_dir`：列出目录
- `read_file`：读取文件
- `write_file`：写入文件
- `stat`：查看元数据
- `find_text`：递归搜索文本
- `apply_patch`：单点文本替换（防止 0 次/多次歧义替换）

### Agent 工具
- `agent_plan`：根据 `goal` 生成执行计划
- `agent_execute`：按 action plan 执行（支持 `dry_run`）
- `agent_run`：调用 **Qwen 3.5** 自主循环（决策 -> 调工具 -> 观察 -> 再决策）直到给出最终答案

## Qwen 3.5 接入

使用 OpenAI-compatible Chat Completions 接口（默认 DashScope compatible-mode）：

```bash
export QWEN_API_KEY="your_api_key"
export QWEN_MODEL="qwen3.5-plus"  # 可改
export QWEN_BASE_URL="https://dashscope.aliyuncs.com/compatible-mode/v1/chat/completions"
```

## 安全设计

- 所有路径都限制在 `MCP_FS_ROOT` 根目录下。
- 阻止路径逃逸（如 `../`）。
- `agent_execute` 与 `agent_run` 都禁止递归调用 `agent_*` 工具。

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
