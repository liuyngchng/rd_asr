package handler

// TaskStatus 定义 ASR 任务的处理阶段
type TaskStatus string

const (
	StatusConverting   TaskStatus = "converting"   // 音频格式转换（ffmpeg）
	StatusSplitting    TaskStatus = "splitting"    // VAD 语音切分
	StatusTranscribing TaskStatus = "transcribing" // ASR 转录（逐段发送 FunASR）
	StatusCompleted    TaskStatus = "completed"    // 转写完成
	StatusFailed       TaskStatus = "failed"       // 异常
)

// Terminated reports whether the task has reached a final state.
func (s TaskStatus) Terminated() bool { return s == StatusCompleted || s == StatusFailed }