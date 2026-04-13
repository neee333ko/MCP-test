# Filesystem MCP Server (Go)

一个运行在 stdio 上的轻量级 MCP Server，提供受限于 sandbox 根目录的文件系统工具。

## 功能

- `list_dir`: 列出目录条目
- `read_file`: 读取文本文件
- `write_file`: 写入文本文件（自动创建父目录）
- `stat`: 查看文件/目录元信息

## Sandbox 根目录

默认使用进程启动目录作为根目录。你也可以通过环境变量覆盖：

```bash
MCP_FS_ROOT=/path/to/sandbox ./mcp-filesystem-server
```

所有路径都会进行校验，禁止访问根目录之外的路径（例如 `../`）。

## 运行

```bash
go build -o mcp-filesystem-server
./mcp-filesystem-server
```

## 与 MCP Host 对接示例

以下是一个简化 JSON-RPC 请求（每行一条 JSON 消息）：

```json
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_dir","arguments":{"path":"."}}}
```
