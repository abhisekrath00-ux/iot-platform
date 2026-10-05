# ai-runtime: llama.cpp's llama-server (CPU only) plus the small airuntime front that adds
# /health /ready /version /model /metrics and a keyed, allow-listed model API.
# Build from the repository root:  docker build -f deploy/ai-runtime/Dockerfile -t iot-ai-runtime .
# The model file is NOT in the image: mount it read-only at /models (see docs/ai-runtime.md).
# No network is needed at run time. Building needs the internet once (source from GitHub).

ARG LLAMA_CPP_COMMIT=d89651a7b205c03c4a0b13cd0646d400dc929f79

FROM debian:bookworm-slim AS llama
ARG LLAMA_CPP_COMMIT
RUN apt-get update && apt-get install -y --no-install-recommends build-essential cmake git ca-certificates && rm -rf /var/lib/apt/lists/*
RUN git clone https://github.com/ggml-org/llama.cpp /src && cd /src && git checkout ${LLAMA_CPP_COMMIT}
# GGML_NATIVE=OFF: a portable build that runs on any x86-64 host the image is copied to.
RUN cd /src && cmake -B build -DCMAKE_BUILD_TYPE=Release -DGGML_NATIVE=OFF -DLLAMA_CURL=OFF -DBUILD_SHARED_LIBS=OFF \
 && cmake --build build --target llama-server -j2

FROM golang:1.25-alpine AS front
WORKDIR /src
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ .
RUN CGO_ENABLED=0 go build -o /bin/airuntime ./cmd/airuntime

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends libgomp1 ca-certificates && rm -rf /var/lib/apt/lists/* && useradd -r -u 10001 ai
COPY --from=llama /src/build/bin/llama-server /usr/local/bin/llama-server
COPY --from=front /bin/airuntime /usr/local/bin/airuntime
COPY deploy/ai-runtime/entrypoint.sh /entrypoint.sh
USER ai
EXPOSE 8090
ENTRYPOINT ["/entrypoint.sh"]
