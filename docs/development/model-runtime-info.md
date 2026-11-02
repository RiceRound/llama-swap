# 模型运行参数管理接口

状态：已实现（待合并）
日期：2026-11-02
调用方：`ai_training/llama_dashboard` 管理面板

## 目的

管理面板需要在模型**尚未加载**时读取启动参数和文件信息，用于预填模型档案、检查配置漂移及评估加载前的显存预算。现有 `/running` 只返回运行中的进程，因此不足以完成这一工作。

只从当前 llama-swap 配置解析命令并查询文件元数据；不启动模型、不读取 GGUF 权重、不计算显存预算，也不创建后台状态缓存。

## 接口

- `GET /api/models`：全部 `models:` 下配置的模型，按真实 ID 排序，包括 `unlisted: true`。响应：`{"models":[...]} `。
- `GET /api/models/{id}`：单个模型。支持包含斜杠的 ID、显式别名及配置中已注册的别名；返回真实 ID；不存在时 HTTP 404。
- 不修改现有 `/running`、`/v1/models`、`/api/events` 或调度逻辑。Peer/Selector 不在本接口的枚举范围。

单模型示例（可选字段按配置实际情况出现）：

`json
{
  "id": "qwen-news",
  "name": "",
  "description": "",
  "state": "stopped",
  "aliases": [],
  "context_length": 32768,
  "runtime": {
    "source": "cmd",
    "model_path": "/data/models/news.gguf",
    "ctx_size": 32768,
    "gpu_layers": "all",
    "cache_type_k": "q8_0",
    "cache_type_v": "q8_0",
    "parallel": 1,
    "batch_size": 1024,
    "ubatch_size": 256,
    "fit": "off",
    "file_exists": true,
    "file_size": 16810714240,
    "file_mtime": "2026-10-25T12:34:56Z"
  }
}
`

## 取值规则

1. 基本字段为 `id`、`name`、`description`、`state`、`aliases`、可选的 `context_length` 和始终存在的 `runtime`。
2. `state` 来自现有 `RunningStatus()`；未启动时返回 `stopped`。不额外推测繁忙状态。
3. `context_length` 优先使用 `capabilities.context`，缺失时沿用现有能力发现结果；仍未知则省略。它与启动参数 `runtime.ctx_size` 含义不同，允许不一致。
4. 有非空 `cmd` 的模型为 `source: cmd`，即使同时配置 `proxy`。调用现成 `SanitizedCommand()` 分词，支持 `--flag value`、`--flag=value` 和已列出的短参数，重复参数取最后一次有效值。解析不了的字段省略，不猜默认值。
5. `cmd` 为空且 `proxy` 非空时，返回 `{"source":"proxy"}`；两者都为空时返回 `{"source":"unknown"}`。此处 `proxy` 可能是系统自动赋予的地址，不代表能够推断上游的启动参数。
6. 允许解析的标志：`--model/-m`（model_path）、`--ctx-size/-c`（ctx_size）、`--n-gpu-layers/--gpu-layers/-ngl`（gpu_layers）、`--cache-type-k/-ctk`、`--cache-type-v/-ctv`、`--parallel/-np`、`--batch-size/-b`、`--ubatch-size/-ub`、`--fit`、`--fit-target`。仅数值字段解析为整数；GPU 层数保留字符串（可为 `all`、`auto` 或数字）。
7. 对已解析的模型文件路径，使用与当前进程启动相同的工作目录语义解析相对路径，并通过 `os.Stat()` 查询普通文件。存在时返回 `file_exists: true`、`file_size`（字节）和 UTC RFC3339 `file_mtime`；不存在、路径不可访问或指向目录时返回 `file_exists: false`，省略大小及修改时间。未解析出路径时省略文件状态。
8. 可选数值字段必须区分缺失和显式设为 0。对包装器或尚未识别的启动参数，只返回有把握解析的字段。

## 安全与边界

- 新接口复用现有 `apiChain` 认证。**注意：未配置 `apiKeys` 的部署，现有认证中间件会放行请求**，管理员须通过 API Key、反向代理或内网访问控制保护模型路径信息；本改动不改变系统原有的认证策略。
- 仅返回白名单字段；不得返回原始 `cmd`、环境变量、API Key、HF Token、代理凭据或未知命令选项。
- 只读取命令字符串及文件元数据；不启动或卸载进程，不打开权重文件，不做 hash、GGUF 头解析、显存估算或额外轮询。
- Windows UNC 网络路径可能导致同步 `stat` 延迟；先不引入缓存、后台线程或超时机制，出现实际性能问题再单独处理。
- 配置热更新已通过重建 Server 生效；接口每次使用当前实例中的配置。文件大小/修改时间可用于轻量漂移提示，**不是强身份校验**。

## 验收

- 从未启动的模型仍能获得配置中的启动参数、文件大小和修改时间。
- 命令的短参数、等号写法、重复参数、显式 0、缺失值处理正确。
- 代理模型和包装器不凭空生成未知配置。
- 支持别名、含斜杠模型 ID、隐藏模型；未知 ID 返回 404。
- 不泄露凭据，不影响现有三个接口，不触发模型启动。
- 测试：`go test -v ./internal/server -run 'TestAPI_Models|TestAPI_ModelByID'`，再运行 `make test-dev`。

## 管理面板后续工作（另一个仓库）

1. 从新接口读取配置，在首次建档时预填路径、上下文、GPU 层数、KV 类型、并行数、batch/ubatch、fit 和文件大小；操作员只需补显示名和显存预算（或机器最小剩余显存阈值）。
2. 未知模型可创建 provisional 档案，但加载前仍必须通过 GPU 监控和显存准入；缺少有效资源依据时拒绝加载。
3. 首次注册/加载不强制 SHA-256；替换、重命名、导入时仍要求 hash。使用路径、文件大小、修改时间及启动参数检测可能的漂移。
4. 实验与原型放在独立 `exploration` 区域，不依赖生产 `modules` 的实验代码。
