FROM golang:1.27-bookworm AS go-build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/voice-recognition ./cmd/server

FROM debian:bookworm-slim AS whisper-build
RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential ca-certificates cmake \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY third_party/whisper.cpp ./whisper.cpp
RUN cmake -S whisper.cpp -B whisper.cpp/build -DCMAKE_BUILD_TYPE=Release \
    && cmake --build whisper.cpp/build -j 6 --target whisper-cli

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates libgomp1 \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --system --uid 10001 --create-home app
COPY --from=go-build /out/voice-recognition /usr/local/bin/voice-recognition
COPY --from=whisper-build /src/whisper.cpp/build/bin/whisper-cli /usr/local/bin/whisper-cli
# whisper.cpp places the CLI's shared libraries in build/bin.  Copy all of
# them: libwhisper also depends on the ggml backend libraries.
COPY --from=whisper-build /src/whisper.cpp/build/bin/ /usr/local/lib/
RUN ldconfig \
    && mkdir -p /app/data/audio && chown -R app:app /app/data
USER app
WORKDIR /app
ENV HTTP_ADDR=:18080 \
    WHISPER_BINARY=/usr/local/bin/whisper-cli \
    WHISPER_MODEL=/models/ggml-large-v3-turbo-q8_0.bin \
    MAX_UPLOAD_MB=100 \
    WHISPER_THREADS=6 \
    WORKERS=2 \
    QUEUE_SIZE=50 \
    UPLOAD_DIR=/app/data/audio
EXPOSE 18080
ENTRYPOINT ["voice-recognition"]
