package funasr

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"rd_asr/internal/vad"

	"github.com/gorilla/websocket"
)

// dialer 内网 FunASR 直连，不走环境变量里的 HTTP 代理。
var dialer = &websocket.Dialer{
	Proxy:             nil,
	HandshakeTimeout:  45 * time.Second,
	EnableCompression: false,
}

type initMsg struct {
	Mode                 string `json:"mode"`
	ChunkSize            [3]int `json:"chunk_size"`
	ChunkInterval        int    `json:"chunk_interval"`
	EncoderChunkLookBack int    `json:"encoder_chunk_look_back"`
	DecoderChunkLookBack int    `json:"decoder_chunk_look_back"`
	AudioFS              int    `json:"audio_fs"`
	WavName              string `json:"wav_name"`
	WavFormat            string `json:"wav_format"`
	IsSpeaking           bool   `json:"is_speaking"`
	Hotwords             string `json:"hotwords"`
	ITN                  bool   `json:"itn"`
}

type resultMsg struct {
	Text      string `json:"text"`
	IsFinal   bool   `json:"is_final"`
	Mode      string `json:"mode"`
	WavName   string `json:"wav_name"`
	Timestamp string `json:"timestamp"`
}

// Ping 探测 FunASR WebSocket 服务是否可连接，返回错误说明。
func Ping(host string, port int) error {
	addr := fmt.Sprintf("ws://%s:%d", host, port)
	conn, resp, err := dialer.Dial(addr, http.Header{
		"Sec-WebSocket-Protocol": {"binary"},
	})
	if err != nil {
		if resp != nil {
			return fmt.Errorf("websocket dial: %w (status=%d)", err, resp.StatusCode)
		}
		return fmt.Errorf("websocket dial: %w", err)
	}
	conn.Close()
	return nil
}

// Send 向 FunASR 发送 PCM 音频并等待 offline 识别结果。
// 完全复刻 Python funasr_wss_client.py 的行为：
//   - 并发 send + recv（recv goroutine 在后台持续收消息，避免连接堵塞）
//   - 发送完所有 chunk 后，最后一帧带 is_speaking=false
//   - 等待 recv 收到 is_final=true 后返回
func Send(ctx context.Context, host string, port int, samples []float32, segIdx int) (string, error) {
	addr := fmt.Sprintf("ws://%s:%d", host, port)
	conn, resp, err := dialer.Dial(addr, http.Header{
		"Sec-WebSocket-Protocol": {"binary"},
	})
	if err != nil {
		if resp != nil {
			return "", fmt.Errorf("websocket dial: %w (status=%d)", err, resp.StatusCode)
		}
		return "", fmt.Errorf("websocket dial: %w", err)
	}
	defer conn.Close()

	// 并发 recv goroutine — 和 Python message() 协程对应，持续接收服务端消息
	resultCh := make(chan string, 1)
	errCh := make(chan error, 1)
	recvDone := make(chan struct{})

	go func() {
		defer close(recvDone)
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				if ctx.Err() != nil {
					errCh <- ctx.Err()
				} else {
					errCh <- fmt.Errorf("recv result: %w", err)
				}
				return
			}
			var r resultMsg
			if err := json.Unmarshal(msg, &r); err != nil {
				continue
			}
			if r.Mode == "offline" {
				slog.Info("asr_infer_done", "segment", segIdx, "text_len", len(r.Text))
				resultCh <- r.Text
				return
			}
			// online / 2pass-online 中间结果忽略，继续读
		}
	}()

	// 发送 — 和 Python record_from_scp() 对应
	init := initMsg{
		Mode: "offline", ChunkSize: [3]int{5, 10, 5}, ChunkInterval: 10,
		EncoderChunkLookBack: 4, DecoderChunkLookBack: 0, AudioFS: 16000,
		WavName: fmt.Sprintf("segment_%d", segIdx), WavFormat: "pcm",
		IsSpeaking: true, Hotwords: "", ITN: true,
	}
	initJSON, _ := json.Marshal(init)
	if err := conn.WriteMessage(websocket.TextMessage, initJSON); err != nil {
		return "", fmt.Errorf("send init: %w", err)
	}

	pcmBytes := vad.Float32ToPCM16(samples)
	stride := 1920
	chunkNum := (len(pcmBytes) - 1) / stride + 1

	for i := 0; i < chunkNum; i++ {
		beg := i * stride
		end := beg + stride
		if end > len(pcmBytes) {
			end = len(pcmBytes)
		}
		if err := conn.WriteMessage(websocket.BinaryMessage, pcmBytes[beg:end]); err != nil {
			return "", fmt.Errorf("send pcm chunk: %w", err)
		}

		// 最后一帧带 is_speaking=false（Python 在循环内判断 i == chunk_num - 1 时发送）
		if i == chunkNum-1 {
			stopJSON, _ := json.Marshal(struct{ IsSpeaking bool }{false})
			if err := conn.WriteMessage(websocket.TextMessage, stopJSON); err != nil {
				return "", fmt.Errorf("send stop: %w", err)
			}
		}
	}

	// 等待 recv goroutine 收到 offline 结果（或出错/取消）
	select {
	case text := <-resultCh:
		return text, nil
	case err := <-errCh:
		return "", err
	case <-ctx.Done():
		conn.Close()
		return "", ctx.Err()
	}
}