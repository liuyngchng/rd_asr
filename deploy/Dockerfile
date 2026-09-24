FROM ubuntu:24.04

# Proxy settings — passed via --build-arg from deploy.sh / CI
ARG HTTP_PROXY
ARG HTTPS_PROXY
ARG http_proxy
ARG https_proxy

# Install ffmpeg (required for audio format conversion / VAD preprocessing)
RUN if [ -n "${HTTP_PROXY:-}" ]; then \
        echo "Acquire::http::Proxy \"$HTTP_PROXY\";" > /etc/apt/apt.conf.d/01proxy; \
        export http_proxy="$HTTP_PROXY"; \
        export https_proxy="${HTTPS_PROXY:-$HTTP_PROXY}"; \
    fi && \
    apt-get update && \
    apt-get install -y --no-install-recommends ffmpeg ca-certificates && \
    rm -rf /var/lib/apt/lists/* /etc/apt/apt.conf.d/01proxy

WORKDIR /opt/asr

# Copy everything assembled by deploy.sh
COPY build_output/rd_asr          ./
COPY build_output/lib/            ./lib/
COPY build_output/templates/      ./templates/
COPY build_output/static/         ./static/
COPY build_output/cfg.yml         ./
COPY build_output/silero_vad.onnx ./

RUN mkdir -p uploads converted results

EXPOSE 19010

ENTRYPOINT ["./rd_asr"]