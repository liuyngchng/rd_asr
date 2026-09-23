package handler

// TaskStatus 定义 ASR 任务的处理阶段
type TaskStatus string

const (
	StatusUploading    TaskStatus = "uploading"    // 上传中（前端已提交，后端正在保存）
	StatusConverting   TaskStatus = "converting"   // 音频格式转换中（ffmpeg）
	StatusSplitting    TaskStatus = "splitting"    // VAD 语音切分中
	StatusTranscribing TaskStatus = "transcribing" // ASR 转录中（逐段发送 FunASR）
	StatusCompleted    TaskStatus = "completed"    // 转写完成
	StatusFailed       TaskStatus = "failed"       // 处理失败
)

// Terminated reports whether the task has reached a final state.
func (s TaskStatus) Terminated() bool { return s == StatusCompleted || s == StatusFailed }