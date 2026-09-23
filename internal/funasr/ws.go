package funasr

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"rd_asr/internal/vad"

	"github.com/gorilla/websocket"
)

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

func Send(host string, port int, samples []float32, segIdx int) (string, error) {
	addr := fmt.Sprintf("ws://%s:%d", host, port)
	conn, resp, err := websocket.DefaultDialer.Dial(addr, http.Header{
		"Sec-WebSocket-Protocol": {"binary"},
	})
	if err != nil {
		if resp != nil {
			return "", fmt.Errorf("websocket dial: %w (status=%d)", err, resp.StatusCode)
		}
		return "", fmt.Errorf("websocket dial: %w", err)
	}
	defer conn.Close()

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
	for i := 0; i < len(pcmBytes); i += stride {
		end := i + stride
		if end > len(pcmBytes) {
			end = len(pcmBytes)
		}
		if err := conn.WriteMessage(websocket.BinaryMessage, pcmBytes[i:end]); err != nil {
			return "", fmt.Errorf("send pcm chunk: %w", err)
		}
	}

	stopJSON, _ := json.Marshal(struct{ IsSpeaking bool }{false})
	if err := conn.WriteMessage(websocket.TextMessage, stopJSON); err != nil {
		return "", fmt.Errorf("send stop: %w", err)
	}

	_, msg, err := conn.ReadMessage()
	if err != nil {
		return "", fmt.Errorf("recv result: %w", err)
	}
	var result resultMsg
	if err := json.Unmarshal(msg, &result); err != nil {
		return "", fmt.Errorf("unmarshal result: %w", err)
	}
	log.Printf("[ws] segment_%d result: text=%q is_final=%v", segIdx, result.Text, result.IsFinal)
	return result.Text, nil
}