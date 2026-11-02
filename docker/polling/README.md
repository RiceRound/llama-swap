# 本机与远程 Docker：拒绝切换并由客户端轮询

在现有模型配置中合并以下设置。本功能不会把 HTTP 503 的请求留在服务端：

```yaml
globalTTL: 0
routing:
  scheduler:
    use: fifo
    settings:
      fifo:
        busyPolicy: reject
        idleTimeout: 300
```

模型自身的 `ttl` 应设为 `0` 或继承 `globalTTL`。正在运行的请求始终受保护；
最后一个 HTTP 请求结束后再保护 300 秒。同一模型收到新请求后重新计时。
只有后续新请求才会触发切换。需要驱逐受保护模型、或与正在切换的模型冲突时，
返回 HTTP 503、`error.code: model_busy` 和 `Retry-After: 5`。客户端等待后重新
发送请求，可增加随机延迟。并发上限仍返回 429。

ComfyUI 的异步出图不等于 HTTP 请求生命周期：超过保护期的后台任务仍可能被
切断。当前版本未接入 ComfyUI 队列状态；生成期间应避免请求冲突模型，或将其
放在有独立 GPU 资源的组中。忽略的 websocket 不延长保护期，普通 HTTP 轮询
则可能使保护期一直延后。手动卸载、TTL 和关闭服务不受此保护限制。

## 本机 Windows

已构建二进制输出在 `build/llama-swap-windows-amd64.exe`（构建文件不提交 Git）。

```powershell
.\build\llama-swap-windows-amd64.exe -config C:/path/to/config.yaml -validate
.\build\llama-swap-windows-amd64.exe -config C:/path/to/config.yaml -listen 127.0.0.1:8080
```

源码重建需要 go.mod 指定的 Go 版本、Node/npm。仓库根目录使用 `make native`
可生成包含 UI 和源码帮助的构建；本次在 Windows 手动执行了等价构建步骤。

## 远程 Linux amd64 / Docker

此镜像仅包含调度服务和 Docker CLI，模型通过配置中的 `docker run` 启动。
先确认远程主机架构为 amd64。将 `build/llama-swap-linux-amd64`、本目录 Dockerfile
和实际 `config.yaml` 传到远程同一工作目录，保持目录层级，然后执行：

```sh
docker build -f docker/polling/Dockerfile -t llama-swap:polling .
docker run -d --name llama-swap-polling --restart unless-stopped \
  --network host \
  -v "$PWD/config.yaml:/config/config.yaml:ro" \
  -v /var/run/docker.sock:/var/run/docker.sock \
  llama-swap:polling
```

`--network host` 用于 Linux：配置中的 proxy 可通过 127.0.0.1 访问模型容器映射
出来的端口。模型容器需配置对应端口映射或使用 host 网络。`cmdStop` 需显式
停止模型容器，例如 `docker stop model-llm`；只停止 Docker CLI 不会释放 GPU。
模型的挂载路径由远程宿主机解析，不能使用本机 Windows 路径。Docker socket
允许管理宿主机容器，应仅供受信任的配置使用。

这里没有填入“100”主机的 IP、SSH 用户、模型路径和 GPU 参数；这些取决于实际
部署环境。本机没有 Docker，镜像构建和远端启动尚未实测。
