ARG CUDA_ARCHITECTURES=89

FROM golang:1.27-bookworm AS go-build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/voice-recognition ./cmd/server

FROM debian:bookworm-slim AS whisper-cpu-build
RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential ca-certificates cmake \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY third_party/whisper.cpp ./whisper.cpp
RUN cmake -S whisper.cpp -B whisper.cpp/build -DCMAKE_BUILD_TYPE=Release \
    && cmake --build whisper.cpp/build -j 6 --target whisper-cli

FROM nvidia/cuda:12.4.1-devel-ubuntu22.04 AS whisper-gpu-build
ARG CUDA_ARCHITECTURES
RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential ca-certificates cmake \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY third_party/whisper.cpp ./whisper.cpp
RUN stub_dir="$(dirname "$(find /usr/local/cuda -path '*/stubs/libcuda.so' -print -quit)")" \
    && ln -s libcuda.so "$stub_dir/libcuda.so.1" \
    && printf '\nif (GGML_CUDA)\n    find_package(CUDAToolkit REQUIRED)\n    target_link_options(${TARGET} PRIVATE "LINKER:--no-as-needed")\n    target_link_libraries(${TARGET} PRIVATE CUDA::cuda_driver)\nendif()\n' >> whisper.cpp/examples/cli/CMakeLists.txt \
    && cmake -S whisper.cpp -B whisper.cpp/build -DCMAKE_BUILD_TYPE=Release \
        -DGGML_CUDA=ON -DCMAKE_CUDA_ARCHITECTURES=$CUDA_ARCHITECTURES \
        "-DCMAKE_EXE_LINKER_FLAGS=-Wl,-rpath-link,$stub_dir" \
    && LIBRARY_PATH="$stub_dir${LIBRARY_PATH:+:$LIBRARY_PATH}" cmake --build whisper.cpp/build -j 6 --target whisper-cli \
    && rm "$stub_dir/libcuda.so.1"

FROM debian:bookworm-slim AS cpu
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates libgomp1 \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --system --uid 10001 --create-home app
COPY --from=go-build /out/voice-recognition /usr/local/bin/voice-recognition
COPY --from=whisper-cpu-build /src/whisper.cpp/build/bin/whisper-cli /usr/local/bin/whisper-cli
COPY --from=whisper-cpu-build /src/whisper.cpp/build/bin/ /usr/local/lib/
RUN ldconfig && mkdir -p /app/data/audio && chown -R app:app /app/data
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

FROM nvidia/cuda:12.4.1-runtime-ubuntu22.04 AS gpu
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates libgomp1 \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --system --uid 10001 --create-home app
COPY --from=go-build /out/voice-recognition /usr/local/bin/voice-recognition
COPY --from=whisper-gpu-build /src/whisper.cpp/build/bin/whisper-cli /usr/local/bin/whisper-cli
COPY --from=whisper-gpu-build /src/whisper.cpp/build/bin/ /usr/local/lib/
RUN ldconfig && mkdir -p /app/data/audio && chown -R app:app /app/data
USER app
WORKDIR /app
ENV HTTP_ADDR=:18080 \
    WHISPER_BINARY=/usr/local/bin/whisper-cli \
    WHISPER_MODEL=/models/ggml-large-v3-turbo-q8_0.bin \
    MAX_UPLOAD_MB=100 \
    WHISPER_THREADS=6 \
    WORKERS=2 \
    QUEUE_SIZE=50 \
    UPLOAD_DIR=/app/data/audio \
    NVIDIA_VISIBLE_DEVICES=all \
    NVIDIA_DRIVER_CAPABILITIES=compute,utility
EXPOSE 18080
ENTRYPOINT ["voice-recognition"]
