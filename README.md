# RD ASR — Go 版语音识别服务

基于 Go 重写的 ASR（Automatic Speech Recognition）服务，完整替代原 Python Flask 版本（`apps/asr/app.py`），对外提供 HTTP API + HTML 前端页面。

## 技术栈

| 组件 | 用途 |
|------|------|
| `net/http` | HTTP 服务（19010 端口） |
| `html/template` | 模板渲染（中文硬编码） |
| `crypto/aes` | AES-128-ECB token 加解密（兼容 Python 版） |
| `modernc.org/sqlite` | 纯 Go SQLite 任务存储 |
| `sherpa-onnx` (CGO) | SileroVAD 语音活动检测 |
| `gorilla/websocket` | FunASR WebSocket 客户端 |
| `ffmpeg` | 音频格式转换（16kHz mono WAV） |
| `gopkg.in/yaml.v3` | YAML 配置解析 |

## 目录结构

```
rd_asr/
├── main.go                     # 入口：装配依赖 + 启动
├── handler/                    # HTTP handler 层
│   ├── handler.go              # Server 结构体 + 共享方法
│   ├── index.go                # GET / + /asr/task 页面
│   ├── static.go               # 静态文件 + webfonts
│   ├── upload.go               # POST /api/upload + 异步处理
│   └── task.go                 # 任务状态/列表/删除/清理/下载
├── internal/
│   ├── config/config.go        # cfg.yml 配置加载
│   ├── token/token.go          # AES-128-ECB token 加解密
│   ├── store/store.go          # SQLite 任务存储
│   ├── i18n/i18n.go            # 中文翻译 JSON Map
│   ├── vad/vad.go              # SileroVAD + WAV 解析
│   └── funasr/ws.go            # FunASR WebSocket 客户端
├── templates/
│   ├── asr_index.html          # 首页模板
│   └── asr_my_task.html        # 任务页模板
├── static/                     # JS/CSS/webfonts（直接复用 Python 版）
├── silero_vad.onnx             # VAD 模型（644KB）
├── cfg.yml.template            # 配置模板（提交 Git）
├── build.sh                    # 构建脚本 → tar.gz
├── deploy.sh                   # Docker 镜像构建
├── Dockerfile                  # Docker 镜像定义
└── README.md
```

## 快速开始

### 前置条件

- Go 1.24+
- ffmpeg（用于音频格式转换）
- FunASR WebSocket 服务（运行时依赖，默认 `127.0.0.1:10095`）

### 配置

编辑 `cfg.yml`：

```yaml
sys:
  name: "语音识别"            # 系统标题
  cypher_key: "abaababaabaababa"  # token 加密密钥（16字节）

funasr:
  host: 127.0.0.1             # FunASR WS 地址
  port: 10095                 # FunASR WS 端口
```

### 构建

```bash
chmod +x build.sh
./build.sh
```

构建产物为 `rd_asr-linux-amd64.tar.gz`。

### 运行

```bash
tar xzf rd_asr-linux-amd64.tar.gz
cd rd_asr
vim cfg.yml              # 配置 FunASR 地址 / token 密钥
./rd_asr
```

### Docker 构建
```

## API 文档

### 页面路由

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/` | 首页（需 `?t=` token） |
| GET | `/asr/task` | 我的任务页（需 token） |

### API 路由

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/upload` | 上传音频（multipart/form-data） |
| GET | `/api/status/{taskId}` | 查询任务状态 |
| POST | `/asr/my/task` | 获取用户任务列表 `{uid}` |
| GET | `/asr/download/{taskId}` | 下载转写结果 |
| POST | `/asr/del/task` | 删除任务 `{task_id}` |
| GET | `/api/tasks?uid=` | 所有任务（兼容旧接口） |
| POST | `/api/clear_tasks` | 清理已完成/失败任务 `{uid}` |

### 支持的文件格式

`.m4a` `.mp3` `.amr` `.wav` `.flac` `.ogg` `.aac`

### 处理流程

```
POST /api/upload
  ↓
1. 校验格式 → 保存原文件
2. 创建 SQLite 任务 (status=converting)
3. 返回 task_id (HTTP 200)
4. ↓ goroutine 异步
5.   ffmpeg → 16kHz mono WAV
6.   读 WAV → []float32
7.   SileroVAD → 语音段检测
8.   for each segment:
9.     WebSocket → FunASR (offline mode)
10.  拼接全部 text → 保存 results/{task_id}.txt
11.  更新状态 status=completed
```

## Token 格式

与 Python 版兼容的 AES-128-ECB 加密 token：

```
payload: {"uid": int, "role": int, "exp": float_timestamp}
encrypt: AES-128-ECB + PKCS7 → base64.urlsafe encode
key:     sys.cypher_key (cfg.yml, 16 bytes)
```

无有效 token 的请求会被 302 重定向到 portal 登录页：`http://127.0.0.1:19000/login?app_source=asr`

## 与原 Python 版的差异

| Python (Flask) | Go |
|---|---|
| Jinja2 模板 + i18n | `html/template` + 中文硬编码 |
| `pycryptodome` AES-ECB | `crypto/aes` 手动 ECB |
| Python `threading` | Go `goroutine` |
| `flask.send_file` | `http.ServeFile` |
| Flask `abort(404)` | `http.NotFound` |
| Python `asyncio` WS | `gorilla/websocket` 同步 API |
| 整个 WAV 文件一次性发 FunASR | VAD 分段后逐段发 FunASR |
| 需 Python 虚拟环境 | 单一二进制（+ .so 文件） |

## License

Copyright (c) 2025 — All rights reserved.