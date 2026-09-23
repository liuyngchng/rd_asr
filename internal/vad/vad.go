package vad

import (
	"encoding/binary"
	"fmt"
	"os"

	sherpa "github.com/k2-fsa/sherpa-onnx-go-linux"
)

type Segment struct {
	Start   int
	Samples []float32
}

func LoadModel(modelPath string) (*sherpa.VoiceActivityDetector, error) {
	config := &sherpa.VadModelConfig{
		SileroVad: sherpa.SileroVadModelConfig{
			Model:              modelPath,
			Threshold:          0.5,
			MinSilenceDuration: 0.5,
			MinSpeechDuration:  0.25,
			MaxSpeechDuration:  300,
			WindowSize:         512,
		},
		SampleRate: 16000,
		NumThreads: 1,
		Provider:   "cpu",
	}
	// buffer 容量按最长音频设置（7200s = 2 小时），避免 circular-buffer 扩容。
	vad := sherpa.NewVoiceActivityDetector(config, 7200.0)
	if vad == nil {
		return nil, fmt.Errorf("failed to create VoiceActivityDetector")
	}
	return vad, nil
}

// chunkSamples 每次喂给 VAD 的样本数（10 秒），避免一次性塞入整段长音频。
const chunkSamples = 10 * 16000

// Detect 分块将音频喂给 VAD，检测语音段并合并到 ≤5 分钟。
// onProgress 每处理完一个 chunk 回调一次（processed/total 为样本数）。
func Detect(vad *sherpa.VoiceActivityDetector, samples []float32, onProgress func(processed, total int)) []Segment {
	total := len(samples)
	for start := 0; start < total; start += chunkSamples {
		end := start + chunkSamples
		if end > total {
			end = total
		}
		vad.AcceptWaveform(samples[start:end])
		if onProgress != nil {
			onProgress(end, total)
		}
	}
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
	return merge(segments, 300*16000)
}

func merge(segments []Segment, maxSamples int) []Segment {
	if len(segments) == 0 {
		return segments
	}
	var merged []Segment
	current := segments[0]
	for i := 1; i < len(segments); i++ {
		next := segments[i]
		if len(current.Samples)+len(next.Samples) <= maxSamples {
			current.Samples = append(current.Samples, next.Samples...)
		} else {
			merged = append(merged, current)
			current = next
		}
	}
	return append(merged, current)
}

func ReadWav(path string) ([]float32, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pcm, err := wavData(data)
	if err != nil {
		return nil, err
	}
	return PCM16ToFloat32(pcm), nil
}

func wavData(data []byte) ([]byte, error) {
	if len(data) < 44 {
		return nil, fmt.Errorf("wav file too short: %d bytes", len(data))
	}
	if string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, fmt.Errorf("not a valid RIFF/WAVE file")
	}
	offset := 12
	for offset+8 <= len(data) {
		chunkID := string(data[offset : offset+4])
		chunkSize := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		chunkDataStart := offset + 8
		if chunkID == "data" {
			end := chunkDataStart + chunkSize
			if end > len(data) {
				end = len(data)
			}
			return data[chunkDataStart:end], nil
		}
		offset = chunkDataStart + chunkSize
		if chunkSize%2 == 1 {
			offset++
		}
	}
	return nil, fmt.Errorf("no data chunk found in wav file")
}

func PCM16ToFloat32(data []byte) []float32 {
	n := len(data) / 2
	samples := make([]float32, n)
	for i := 0; i < n; i++ {
		v := int16(binary.LittleEndian.Uint16(data[i*2 : i*2+2]))
		samples[i] = float32(v) / 32768.0
	}
	return samples
}

func Float32ToPCM16(samples []float32) []byte {
	data := make([]byte, len(samples)*2)
	for i, s := range samples {
		v := int16(s * 32767.0)
		binary.LittleEndian.PutUint16(data[i*2:i*2+2], uint16(v))
	}
	return data
}