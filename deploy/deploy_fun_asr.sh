

# stop
docker stop myfunasr
# clear
docker rm myfunasr
# start
docker run -p 10095:10095 -dit --privileged=true --name myfunasr \
  -v /data/funasr-runtime-resources/models:/workspace/models \
  -e MODELSCOPE_DISABLE_DOWNLOAD=1 \
  -e HF_HUB_DISABLE_TELEMETRY=1 \
  -e FUNASR_DISABLE_DOWNLOAD=1 \
  funasr-with-ffmpeg:runtime-sdk-cpu-0.4.7 \
  /bin/bash -c "cd /workspace/FunASR/runtime/websocket/build/bin && ./funasr-wss-server --model-dir /workspace/models/damo/speech_paraformer-large-vad-punc_asr_nat-zh-cn-16k-common-vocab8404-onnx --vad-dir /workspace/models/damo/speech_fsmn_vad_zh-cn-16k-common-onnx --punc-dir /workspace/models/damo/punc_ct-transformer_cn-en-common-vocab471067-large-onnx --itn-dir /workspace/models/thuduj12/fst_itn_zh --lm-dir /workspace/models/damo/speech_ngram_lm_zh-cn-ai-wesp-fst --port 10095 --certfile '' --decoder-thread-num 4 --io-thread-num 1 --model-thread-num 4"

# 查看日志
docker logs -f myfunasr