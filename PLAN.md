# Go 版 ASR 服务 — 完整替代 Flask 方案

## 目标

用 Go 完整替代 `apps/asr/app.py` + `asr_util.py` + `wss_client.py`：
- HTTP 服务（19010 端口）
- Portal 登录 token 识别（`?t=` AES-128-ECB + PKCS7 解密）
- 静态文件 + WebFonts 服务
- SQLite 任务存储
- Go `html/template` 模板渲染（中文硬编码，不搞 i18n）
- 文件上传 / 任务状态 / 我的任务 / 下载 / 删除 API
- VAD 切片（sherpa-onnx SileroVAD）
- FunASR WebSocket 分段识别

## 目录结构

```
/home/rd/workspace/rd_asr/
├── PLAN.md                     # 本文件
├── go.mod
├── go.sum
├── main.go                     # 入口：HTTP 路由注册 + 启动
├── config.go                   # 配置加载（cfg.yml 解析）
├── token.go                    # AES-128-ECB token 加解密
├── db.go                       # SQLite 任务存储
├── handler_index.go            # GET / — 首页（模板渲染）
├── handler_upload.go           # POST /api/upload + ffmpeg + VAD+ASR
├── handler_task.go             # GET/POST 任务相关 API
├── handler_download.go         # GET /asr/download/:task_id
├── handler_static.go           # GET /static/* + /webfonts/*
├── vad.go                      # SileroVAD 加载 + 语音段检测
├── ws_client.go                # FunASR WebSocket 客户端（单段发送+接收）
├── templates/
│   ├── asr_index.html          # 首页模板（Go 语法，中文硬编码）
│   └── asr_my_task.html        # 任务页模板（Go 语法，中文硬编码）
├── static/
│   ├── asr.css
│   ├── asr.js
│   ├── asr.my.task.css
│   ├── asr.my.task.js
│   ├── i18n.js                 # 多语言前端支持（依赖 window.__I18N__）
│   └── ...                     # webfonts/
├── silero_vad.onnx             # 拷贝自 ~/.voicenote/models/silero_vad.onnx (644KB)
└── cfg.yml                     # 配置文件（可复用现有）
```

## 一、Token 认证（AES-128-ECB）

### 加密方式（来自 `cm_utils.py`）

```
密钥: sys.cypher_key = "abaababaabaababa" (16 bytes)
算法: AES-128-ECB + PKCS7 padding + base64.url-safe encode

加密: payload_json → AES.ECB.Encrypt(PKCS7(payload_json)) → base64.URLSafe.Encode
解密: base64.URLSafe.Decode(token) → AES.ECB.Decrypt → PKCS7.Unpad → JSON

payload: {"uid": int, "role": int, "exp": float_timestamp}
```

### Go 实现 (`token.go`)

```go
package main

import (
    "crypto/aes"
    "encoding/base64"
    "encoding/json"
    "time"
)

type TokenPayload struct {
    UID  int     `json:"uid"`
    Role int     `json:"role"`
    Exp  float64 `json:"exp"`
}

// Go 标准库无 ECB 模式，手动实现块级加密循环（~15 行）
func encryptECB(plaintext, key []byte) ([]byte, error) { ... }
func decryptECB(ciphertext, key []byte) ([]byte, error) { ... }

func PKCS7Pad(data []byte, blockSize int) []byte { ... }
func PKCS7Unpad(data []byte, blockSize int) ([]byte, error) { ... }

func DecodeToken(token string, key []byte) (*TokenPayload, error) {
    // 1. base64.RawURLEncoding.DecodeString(token)
    // 2. decryptECB(ciphertext, key)
    // 3. PKCS7Unpad
    // 4. json.Unmarshal → TokenPayload
    // 5. Check exp > time.Now()
}
```

### 验证中间件

所有页面路由从 `?t=` 读取 token → `DecodeToken` → 验证通过 → 提取 uid/role。

无效/过期 token → HTTP 302 跳转到 portal 登录页：
```
http://127.0.0.1:19000/login?app_source=asr
```

## 二、HTTP 路由

| 方法 | 路径 | 处理函数 | 说明 |
|------|------|---------|------|
| GET | `/` | `handleIndex` | 验证 `?t=` token → 渲染首页 |
| GET | `/asr/task` | `handleTaskPage` | 验证 token → 渲染任务页 |
| GET | `/static/*` | `handleStatic` | 优先 apps/asr/static，其次 common/static |
| GET | `/webfonts/*` | `handleWebfonts` | 字体文件 |
| POST | `/api/upload` | `handleUpload` | 上传音频 → ffmpeg → VAD+ASR |
| GET | `/api/status/:taskId` | `handleStatus` | 查询任务状态 |
| POST | `/asr/my/task` | `handleMyTasks` | 用户任务列表(`{uid}` → JSON) |
| GET | `/asr/download/:taskId` | `handleDownload` | 下载转写结果 |
| POST | `/asr/del/task` | `handleDeleteTask` | `{task_id}` → 删除 |
| GET | `/api/tasks?uid=` | `handleAllTasks` | 所有任务（兼容旧接口） |
| POST | `/api/clear_tasks` | `handleClearTasks` | 清理已完成（`{uid}`）|

## 三、模板渲染（html/template）

### Jinja2 → Go template 转换

| Jinja2 | Go html/template |
|--------|-----------------|
| `{{ lang }}`, `{{ dir }}` | `{{.Lang}}`, `{{.Dir}}` |
| `{{ sys_name }}` | `{{.SysName}}` |
| `{{ _('asr.welcome') }}` | 直接写中文: `上传音频文件，自动转换为文字` |
| `{{ uid }}`, `{{ t }}` | `{{.UID}}`, `{{.Token}}` |
| `{% if dir == 'rtl' %}` | 去掉（中文只有 ltr） |
| `{{ i18n_js \| safe }}` | `{{.I18NJS}}`（注入中文翻译 JSON） |

### 渲染上下文

```go
type PageContext struct {
    Lang      string       // 固定 "zh"
    Dir       string       // 固定 "ltr"
    SysName   string       // 系统名称
    UID       string       // 用户 ID
    Token     string       // 原始 token 串
    AppSource string       // "asr"
    I18NJS    template.JS  // window.__I18N__ 中文翻译 JSON
}
```

## 四、JS 前端兼容性

### 现有 JS 文件对 i18n 的依赖

`asr.js` 和 `asr.my.task.js` 通过以下方式使用翻译：
```js
__('asr.processing')              // 简单翻译
__fmt_named('asr.unsupported_format', {name: file.name})  // 带参数翻译
```

这些函数定义在 `static/i18n.js` 中，依赖 `window.__I18N__`。

### 最小兼容方案

模板中注入一个中文翻译 JSON 对象 `window.__I18N__`，同时注入 `window.__LANG__ = "zh"`，现有 JS 无需任何修改：

```html
<script>
window.__I18N__ = {
    "asr.processing": "正在处理 {name}...",
    "asr.upload_success": "上传成功",
    "asr.unsupported_format": "不支持的文件格式: {name}",
    "asr.process_failed": "处理失败: {msg}",
    "asr.upload_failed": "上传失败: {msg}",
    "asr.transcription_done": "转写完成！",
    "asr.no_result": "无识别结果",
    "asr.download_result": "下载结果",
    "asr.transcribe_failed": "转写失败: {msg}",
    "asr.converting_format": "正在处理 {name}: 转换音频格式...",
    "asr.waiting_server": "正在处理 {name}: 数据已发送，等待服务端处理中（{elapsed}）",
    "asr.processing_pct": "正在处理 {name}: {pct}%",
    "asr.recognizing": "正在处理 {name}: 语音识别中...",
    "asr.elapsed_format": "{m}分{s}秒",
    "asr.download_failed": "下载失败: ",
    "asr.history_title": "历史记录",
    "asr.transcribed_done": "已转写完成",
    "asr.re_download": "重新下载",
    "asr.history_cleared": "历史记录已清空",
    "asr.clear_confirm": "确定要清空所有历史记录吗？",
    "asr.my_tasks_btn": "我的任务",
    "asr.task_page_title": "ASR 转写任务",
    "asr.task_page_desc": "查看和管理您的语音转写任务",
    "asr.col_filename": "文件名",
    "asr.col_create_time": "创建时间",
    "asr.col_status": "状态",
    "asr.col_progress": "进度",
    "asr.col_download": "下载",
    "asr.col_actions": "操作",
    "asr.status_converting": "转换中",
    "asr.status_sending": "发送中",
    "asr.status_transcribing": "转录中",
    "asr.status_completed": "已完成",
    "asr.status_failed": "失败",
    "asr.clear_completed": "清理已完成",
    "asr.clear_completed_confirm": "确定要清理所有已完成和失败的任务吗？",
    "asr.delete_confirm": "确定要删除 {name} 吗？",
    "asr.view_tasks_hint": "可在「我的任务」页面查看进度和下载结果",
    "asr.selected_files": "已选择文件:",
    "asr.note_placeholder": "可选：输入额外说明或备注...",
    "asr.audio_to_text": "音频转文字",
    "asr.select_file_btn": "选择文件",
    "asr.upload_hint": "点击上传按钮选择音频文件，系统将自动转换为文字",
    "asr.welcome": "上传音频文件，自动转换为文字",
    "asr.welcome_desc": "支持 MP3、M4A、AMR、WAV、FLAC、OGG、AAC 等格式",
    "asr.supported_formats": "支持格式: MP3, M4A, AMR, WAV, FLAC, OGG, AAC",
    "asr.user_role": "用户",
    "asr.system_role": "系统",
    "common.loading": "加载中...",
    "common.error_retry": "操作失败，请重试",
    "common.save": "保存",
    "common.cancel": "取消",
    "common.confirm": "确认",
    "common.delete": "删除",
    "common.search": "搜索",
    "common.submit": "提交",
    "common.back": "返回",
    "common.download": "下载",
    "common.close": "关闭",
    "common.refresh": "刷新",
    "common.preview": "预览",
    "common.retry": "重试",
    "common.done": "完成",
    "common.no": "No.",
    "common.unknown_time": "未知时间",
    "common.no_title": "无标题",
    "common.unknown_type": "未知类型",
    "common.status_running": "处理中...",
    "common.status_completed": "已完成",
    "common.network_error": "网络错误",
    "common.unknown_error": "未知错误",
    "common.file_not_found": "文件不存在",
    "common.read_file_error": "读取文件时出错: {msg}",
    "common.empty_task_list": "暂无任务",
    "common.empty_task_desc": "您还没有创建任何任务",
    "common.task_fetch_failed": "获取任务列表失败",
    "common.submit_failed": "提交失败",
    "common.delete_failed": "删除失败",
    "common.delete_success": "删除成功",
    "common.refresh_failed": "刷新失败，请重试",
    "common.retry_failed": "重试失败，请再试一次",
    "common.delete_failed_retry": "删除失败，请重试",
    "common.request_failed": "请求失败:",
    "common.fetch_failed": "获取失败",
    "common.auto_refresh_on": "自动刷新中",
    "common.upload": "上传",
};
window.__LANG__ = "zh";
</script>
```

### 翻译 JSON 来源

从 `common/i18n/_translations.py` 中提取 `asr`、`common`、`auth`、`vdb` section 的 `zh` 字段，拼成上述 JSON。写一个一次性 Python 脚本跑一次即可，手动拷贝结果到模板里（反正不会再改）。

或者更简单的——直接在 Go 代码里硬编码这个 `map[string]string`，模板渲染时序列化为 JSON 注入。

## 五、SQLite 任务存储

完全复刻 `asr_util.py` 中的 `ASRTaskStore`：

```sql
CREATE TABLE IF NOT EXISTS asr_tasks (
    task_id        TEXT PRIMARY KEY,
    uid            INTEGER NOT NULL,
    original_filename TEXT NOT NULL,
    original_path  TEXT NOT NULL DEFAULT '',
    converted_path TEXT NOT NULL DEFAULT '',
    status         TEXT NOT NULL DEFAULT 'converting',
    result_text    TEXT,
    progress       INTEGER DEFAULT 0,
    error          TEXT,
    created_at     TEXT NOT NULL DEFAULT (datetime('now','localtime')),
    updated_at     TEXT NOT NULL DEFAULT (datetime('now','localtime'))
);
```

Go 使用 `modernc.org/sqlite`（纯 Go，无需 CGO，与 sherpa-onnx 的 CGO 不冲突）。

## 六、文件上传 + ASR 处理流程

### 完整流程

```
POST /api/upload (multipart/form-data)
  │
  ▼
1. 校验文件格式 (.m4a/.mp3/.amr/.wav/.flac/.ogg/.aac)
2. 保存到 uploads/{uuid}.{ext}
3. 创建 SQLite 任务 (status=converting)
4. 返回 {'task_id': ..., 'status': 'converting'}
5. ↓ goroutine 异步
6.   ffmpeg → 16kHz/mono WAV → converted/{name}_{uuid}.wav (status=processing, progress=0)
7.   读 WAV → []float32
8.   SileroVAD.AcceptWaveform(samples) → Flush → Front/Pop 循环
     合并小段 ≤ 5 分钟/段 → 得到 N 个 SpeechSegment
9.   for each segment (更新 progress = i/N*100%):
10.      WebSocket → FunASR offline mode
11.      收集每段 text → 拼接
12.  保存到 results/{task_id}.txt (status=completed, progress=100)
```

### ffmpeg 调用

```go
cmd := exec.Command("ffmpeg",
    "-i", inputPath,
    "-acodec", "pcm_s16le",
    "-ar", "16000",
    "-ac", "1",
    "-y", outputPath,
)
```

### VAD 切片（`vad.go`）

```go
import "github.com/k2-fsa/sherpa-onnx-go-linux/sherpa_onnx"

func loadVAD(modelPath string) (*sherpa_onnx.VoiceActivityDetector, error) {
    config := &sherpa_onnx.VadModelConfig{
        SileroVad: sherpa_onnx.SileroVadModelConfig{
            Model:              modelPath,
            Threshold:          0.5,
            MinSilenceDuration: 0.5,
            MinSpeechDuration:  0.25,
            MaxSpeechDuration:  300,    // 5 分钟
            WindowSize:         512,    // 32ms @ 16kHz
        },
        SampleRate: 16000,
        NumThreads: 1,
        Provider:   "cpu",
    }
    return sherpa_onnx.NewVoiceActivityDetector(config, 60.0), nil
}

func detectSegments(vad *sherpa_onnx.VoiceActivityDetector, samples []float32) []Segment {
    vad.AcceptWaveform(samples)
    vad.Flush()
    var segments []Segment
    for !vad.IsEmpty() {
        seg := vad.Front()
        segments = append(segments, Segment{
            Start:   seg.Start,
            Samples: seg.Samples,
        })
        vad.Pop()
    }
    return mergeSmallSegments(segments, 300*16000) // 合并到 ≤5min
}
```

### WebSocket 客户端（`ws_client.go`）

使用 `github.com/gorilla/websocket`，复刻 Python `wss_client.py` 的 offline 模式：

```go
func asrSegment(host string, port int, pcmSamples []float32, segIdx int) (string, error) {
    conn, _, err := websocket.DefaultDialer.Dial(
        fmt.Sprintf("ws://%s:%d", host, port),
        http.Header{"Sec-WebSocket-Protocol": {"binary"}},
    )
    defer conn.Close()

    // 1. 发送初始化 JSON
    initMsg := map[string]interface{}{
        "mode":                    "offline",
        "chunk_size":              [3]int{5, 10, 5},
        "chunk_interval":          10,
        "encoder_chunk_look_back": 4,
        "decoder_chunk_look_back": 0,
        "audio_fs":                16000,
        "wav_name":                fmt.Sprintf("segment_%d", segIdx),
        "wav_format":              "pcm",
        "is_speaking":             true,
        "hotwords":                "",
        "itn":                     true,
    }
    conn.WriteMessage(websocket.TextMessage, jsonBytes(initMsg))

    // 2. 分块发送 PCM
    stride := int(60 * 10 / 10 / 1000.0 * 16000 * 2) // 1920 bytes
    pcmBytes := float32ToPCMBytes(pcmSamples)
    for i := 0; i < len(pcmBytes); i += stride {
        end := min(i+stride, len(pcmBytes))
        conn.WriteMessage(websocket.BinaryMessage, pcmBytes[i:end])
    }

    // 3. 发送终止
    conn.WriteMessage(websocket.TextMessage, jsonBytes(map[string]bool{"is_speaking": false}))

    // 4. 接收结果
    _, msg, _ := conn.ReadMessage()
    var result map[string]interface{}
    json.Unmarshal(msg, &result)
    return result["text"].(string), nil
}
```

### 进度通知

goroutine 直接写 SQLite `asr_tasks.progress`，前端通过 `/api/status/{taskId}` 轮询。

## 七、依赖的 Go 模块

| 模块 | 用途 | 来源 |
|------|------|------|
| `github.com/k2-fsa/sherpa-onnx-go-linux` | SileroVAD CGO 绑定 | 已有 go mod cache |
| `github.com/gorilla/websocket` | WebSocket 客户端 | 已有 go mod cache |
| `modernc.org/sqlite` | SQLite（纯 Go） | 需安装 |
| `gopkg.in/yaml.v3` | YAML 配置解析 | 需安装 |
| `github.com/google/uuid` | UUID 生成 | 需安装 |
| 标准库 `net/http`, `html/template`, `crypto/aes` | HTTP/模板/加密 | Go 内置 |

## 八、模板转换要点

两个模板从 Jinja2 转为 Go `html/template`：

1. **去掉 i18n 函数调用**：`{{ _('asr.xxx') }}` → 直接写中文字符串
2. **去掉 RTL 分支**：中文不需要 `{% if dir == 'rtl' %}`
3. **变量语法**：`{{ uid }}` → `{{.UID}}`，`{{ t }}` → `{{.Token}}`
4. **注入 I18N JS**：用一个硬编码的 `map[string]string` 序列化为 JSON 注入模板

## 九、实现步骤

| 步骤 | 内容 | 文件 |
|------|------|------|
| 1 | 创建 go.mod + install 依赖 | `go.mod`, `go.sum` |
| 2 | 拷贝 silero_vad.onnx | `silero_vad.onnx` |
| 3 | 实现 token 加解密 | `token.go` |
| 4 | 实现 SQLite 存储 | `db.go` |
| 5 | 准备中文翻译 JSON Map（硬编码在 Go 常量里） | `i18n_map.go` |
| 6 | 转换模板 Jinja2→Go（中文硬编码） | `templates/*.html` |
| 7 | 拷贝静态文件（JS/CSS 复用不改） | `static/` |
| 8 | 实现静态文件路由 | `handler_static.go` |
| 9 | 实现首页路由 | `handler_index.go` |
| 10 | 实现任务页路由 | `handler_task.go` |
| 11 | 实现上传+处理路由（异步 goroutine） | `handler_upload.go` |
| 12 | 实现下载路由 | `handler_download.go` |
| 13 | 实现 VAD 切片 | `vad.go` |
| 14 | 实现 WS 客户端 | `ws_client.go` |
| 15 | 实现配置加载 | `config.go` |
| 16 | 组装 main.go，编译 + 端到端测试 | `main.go` |

## 十、风险 & 应对

| 风险 | 应对 |
|------|------|
| AES-ECB Go 无标准实现 | 手动实现 ECB 块级加解密（~15 行） |
| sherpa-onnx VAD 离线模式 AcceptWaveform/Flush/Front API 行为 | 先写最小测试验证 |
| modernc.org/sqlite 纯 Go | 与 sherpa-onnx 的 CGO 可能冲突（链接器），实测验证 |
| gorilla/websocket 无代理支持 | 内网直连 FunASR，不需要代理 |
| Go template 与 Jinja2 语法差异 | 模板简单，差异有限，逐个字段转换 |
| JS 文件依赖 `__()` 函数 | 注入中文 `window.__I18N__` + 保留 `i18n.js`，JS 零改动 |