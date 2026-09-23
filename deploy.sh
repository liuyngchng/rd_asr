#!/bin/bash
set -euo pipefail

APP_NAME="rd_asr"
STAGE_DIR="build_output"
IMAGE_NAME="${APP_NAME}"
IMAGE_TAG="${IMAGE_TAG:-1.0}"

# ── Proxy settings (internal network) ──────────────────────────
export HTTP_PROXY="${HTTP_PROXY:-http://proxy3.bj.petrochina:8080}"
export HTTPS_PROXY="${HTTPS_PROXY:-http://proxy3.bj.petrochina:8080}"
export GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"

echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  Deploy ${APP_NAME} via Docker (from source)"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

# ── 1. Clean & prepare staging directory ───────────────────────
echo "[1/4] Preparing staging directory ..."
rm -rf "${STAGE_DIR}"
mkdir -p "${STAGE_DIR}/lib"

# ── 2. Copy C shared libraries from sherpa-onnx ───────────────
SHERPA_DIR=$(go list -m -f '{{.Dir}}' github.com/k2-fsa/sherpa-onnx-go-linux 2>/dev/null || echo "")
if [ -n "${SHERPA_DIR}" ] && [ -d "${SHERPA_DIR}/lib/x86_64-unknown-linux-gnu" ]; then
    cp -v "${SHERPA_DIR}/lib/x86_64-unknown-linux-gnu/"*.so "${STAGE_DIR}/lib/"
else
    echo "ERROR: sherpa-onnx shared libraries not found"
    exit 1
fi

# ── 3. Build binary (CGO) ──────────────────────────────────────
echo "[2/4] Building binary ..."
CGO_ENABLED=1 go build \
    -trimpath \
    -ldflags="-s -w -extldflags '-Wl,-rpath,\$ORIGIN/lib'" \
    -o "${STAGE_DIR}/${APP_NAME}" \
    .

# ── 4. Copy runtime assets ─────────────────────────────────────
echo "[3/4] Assembling runtime assets ..."
cp -r templates      "${STAGE_DIR}/"
cp -r static         "${STAGE_DIR}/"
cp silero_vad.onnx   "${STAGE_DIR}/"

# cfg.yml 策略：从 template 生成（本地测试用的 cfg.yml 不进镜像）
if [ -f "cfg.yml.template" ]; then
    cp cfg.yml.template "${STAGE_DIR}/cfg.yml"
else
    echo "ERROR: 缺少 cfg.yml.template"
    exit 1
fi

mkdir -p "${STAGE_DIR}/uploads" "${STAGE_DIR}/converted" "${STAGE_DIR}/results"

# ── 5. Build Docker image ──────────────────────────────────────
echo "[4/4] Building Docker image: ${IMAGE_NAME}:${IMAGE_TAG} ..."
docker build --rm -f Dockerfile \
    --build-arg HTTP_PROXY="${HTTP_PROXY}" \
    --build-arg HTTPS_PROXY="${HTTPS_PROXY}" \
    --build-arg http_proxy="${HTTP_PROXY}" \
    --build-arg https_proxy="${HTTPS_PROXY}" \
    -t "${IMAGE_NAME}:${IMAGE_TAG}" .

# ── 6. Summary ─────────────────────────────────────────────────
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  Docker image: ${IMAGE_NAME}:${IMAGE_TAG}"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
echo "  Run container:"
echo "    docker run -d \\"
echo "      -p 19010:19010 \\"
echo "      -v \$(pwd)/cfg.yml:/opt/asr/cfg.yml \\"
echo "      -v \$(pwd)/uploads:/opt/asr/uploads \\"
echo "      -v \$(pwd)/converted:/opt/asr/converted \\"
echo "      -v \$(pwd)/results:/opt/asr/results \\"
echo "      --name ${APP_NAME} \\"
echo "      ${IMAGE_NAME}:${IMAGE_TAG}"
echo ""
echo "  View logs:"
echo "    docker logs -f ${APP_NAME}"
echo ""
echo "  Stop & remove:"
echo "    docker stop ${APP_NAME} && docker rm ${APP_NAME}"
echo ""

docker images "${IMAGE_NAME}:${IMAGE_TAG}" 2>/dev/null