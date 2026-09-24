#!/bin/bash

# 生成测试的5min wav 音频文件
ffmpeg -i your_input_audio_media.m4a test_5min.wav
# 进入 funasr 的容器内部
docker exec -it myfunasr bash
# 进入服务的测试目录
cd /workspace/FunASR/runtime/python/websocket
# 执行测试脚本， 需要事先将 /home/rd/workspace/rd_asr/test_5min.wav 拷贝到容器内
python ./funasr_wss_client.py --host "127.0.0.1" --port 10095 \
  --ssl 0 --mode offline \
  --audio_in "/home/rd/workspace/rd_asr/test_5min.wav" \
  --output_dir "./results"