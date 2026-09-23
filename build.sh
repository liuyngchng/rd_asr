#!/bin/bash
set -euo pipefail

APP_NAME="rd_asr"
TOP_DIR="${APP_NAME}"
TARBALL="${APP_NAME}-linux-amd64.tar.gz"

# ── 检查代理 ──────────────────────────────────────────────────
if [ -z "${HTTP_PROXY:-}" ] && [ -z "${http_proxy:-}" ]; then
    echo "⚠  未设置 HTTP_PROXY，如果处在内网环境请先设置代理："
    echo ""
    echo "   export HTTP_PROXY=http://your-proxy:port"
    echo "   export HTTPS_PROXY=http://your-proxy:port"
    echo "   export GOPROXY=https://goproxy.cn,direct"
    echo ""
fi

echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  Building ${APP_NAME} release tarball"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

# ── 1. Clean & prepare staging directory ───────────────────────
rm -rf "${TOP_DIR}" "${TARBALL}"
mkdir -p "${TOP_DIR}/lib"

# ── 2. Copy C shared libraries from sherpa-onnx ───────────────
SHERPA_DIR=$(go list -m -f '{{.Dir}}' github.com/k2-fsa/sherpa-onnx-go-linux 2>/dev/null || echo "")
if [ -n "${SHERPA_DIR}" ] && [ -d "${SHERPA_DIR}/lib/x86_64-unknown-linux-gnu" ]; then
    cp -v "${SHERPA_DIR}/lib/x86_64-unknown-linux-gnu/"*.so "${TOP_DIR}/lib/"
else
    echo "ERROR: sherpa-onnx shared libraries not found"
    exit 1
fi

# ── 3. Build binary (CGO) ──────────────────────────────────────
CGO_ENABLED=1 go build \
    -trimpath \
    -ldflags="-s -w -extldflags '-Wl,-rpath,\$ORIGIN/lib'" \
    -o "${TOP_DIR}/${APP_NAME}" \
    .

# ── 4. Copy runtime assets ─────────────────────────────────────
cp -r templates      "${TOP_DIR}/"
cp -r static         "${TOP_DIR}/"
cp silero_vad.onnx   "${TOP_DIR}/"

if [ -f "cfg.yml.template" ]; then
    cp cfg.yml.template "${TOP_DIR}/cfg.yml"
else
    echo "ERROR: 缺少 cfg.yml.template"
    exit 1
fi

mkdir -p "${TOP_DIR}/uploads" "${TOP_DIR}/converted" "${TOP_DIR}/results"

# ── 5. Pack tarball ────────────────────────────────────────────
tar czf "${TARBALL}" "${TOP_DIR}"
rm -rf "${TOP_DIR}"

# ── 6. Summary ─────────────────────────────────────────────────
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  Release: ${TARBALL}"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
ls -lh "${TARBALL}"
echo ""
echo "  Usage:"
echo "    tar xzf ${TARBALL}"
echo "    cd ${APP_NAME}"
echo "    vim cfg.yml            # 配置 FunASR 地址 / token 密钥"
echo "    ./${APP_NAME}"
echo ""