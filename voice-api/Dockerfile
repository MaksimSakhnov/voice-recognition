FROM golang:1.27-bookworm AS go-build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/voice-recognition ./cmd/server

FROM debian:bookworm-slim AS whisper-build
RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential ca-certificates cmake git \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /src
RUN git clone --depth 1 https://github.com/ggml-org/whisper.cpp.git
RUN cmake -S whisper.cpp -B whisper.cpp/build -DCMAKE_BUILD_TYPE=Release \
    && cmake --build whisper.cpp/build -j 6 --target whisper-cli

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates libgomp1 \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --system --uid 10001 --create-home app
COPY --from=go-build /out/voice-recognition /usr/local/bin/voice-recognition
COPY --from=whisper-build /src/whisper.cpp/build/bin/whisper-cli /usr/local/bin/whisper-cli
RUN mkdir -p /app/data/audio && chown -R app:app /app/data
USER app
WORKDIR /app
ENV HTTP_ADDR=:8080 \
    WHISPER_BINARY=/usr/local/bin/whisper-cli \
    WHISPER_MODEL=/models/ggml-large-v3-turbo-q8_0.bin \
    MAX_UPLOAD_MB=100 \
    WHISPER_THREADS=6 \
    UPLOAD_DIR=/app/data/audio
EXPOSE 8080
ENTRYPOINT ["voice-recognition"]
