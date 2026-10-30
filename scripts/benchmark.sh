#!/usr/bin/env bash
set -euo pipefail

# Отправляет один фиксированный набор WAV в запущенный API и выводит метрики STT.
# Нужны: bash, curl, jq, GNU date. Необязателен: ps (для MEMORY_PID).
# RUN_ALL=1 дополнительно запускает Docker-сервис с WORKERS=1,2,4 и измеряет
# весь контейнер, включая дочерние процессы whisper-cli. Укажите
# BENCHMARK_MODE=gpu для необязательного варианта CUDA Compose.

api_url="${API_URL:-http://localhost:18080}"
audio_dir="${AUDIO_DIR:?set AUDIO_DIR to a directory containing WAV files}"
workers="${WORKERS:-unknown}"
poll_seconds="${POLL_SECONDS:-0.2}"
memory_pid="${MEMORY_PID:-}"
memory_container="${MEMORY_CONTAINER:-}"
run_all="${RUN_ALL:-0}"
benchmark_mode="${BENCHMARK_MODE:-cpu}"
script_path="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)/$(basename -- "${BASH_SOURCE[0]}")"
project_dir="$(cd -- "$(dirname -- "$script_path")/.." && pwd)"
results_file="${BENCHMARK_RESULTS_FILE:-$project_dir/benchmark-result-$benchmark_mode.md}"

compose_files=()
case "$benchmark_mode" in
  cpu) ;;
  gpu) compose_files=(-f docker-compose.yml -f docker-compose.gpu.yml) ;;
  *) echo "BENCHMARK_MODE must be cpu or gpu" >&2; exit 2 ;;
esac

compose() { docker compose "${compose_files[@]}" "$@"; }

wait_for_api() {
  local attempts=60
  while (( attempts > 0 )); do
    if curl --fail --silent "$api_url/healthz" >/dev/null; then
      return 0
    fi
    sleep 1
    ((attempts--))
  done
  echo "API did not become ready at $api_url" >&2
  return 1
}

run_all_workers() {
  command -v docker >/dev/null
  [[ -d "$audio_dir" ]] || { echo "AUDIO_DIR does not exist: $audio_dir" >&2; exit 1; }

  table_file="$(mktemp)"
  trap 'rm -f "$table_file"' RETURN
  printf '| Workers | Total time | Avg job latency | Peak memory |\n' >"$table_file"
  printf '| ---: | ---: | ---: | ---: |\n' >>"$table_file"
  dataset_count="$(find "$audio_dir" -maxdepth 1 -type f -iname '*.wav' -print | wc -l | tr -d ' ')"
  model="unknown"
  whisper_threads="unknown"
  queue_size="unknown"
  gpu="not used (CPU mode)"
  echo "building voice-api image once" >&2
  compose build voice-api >&2
  for worker_count in 1 2 4; do
    echo "starting benchmark with WORKERS=$worker_count" >&2
    WORKERS="$worker_count" compose up -d --force-recreate voice-api >&2
    container_id="$(compose ps -q voice-api)"
    [[ -n "$container_id" ]] || { echo "voice-api container was not created" >&2; exit 1; }
    wait_for_api
    server_pid="$(docker inspect --format '{{.State.Pid}}' "$container_id")"
    echo "WORKERS=$worker_count: container=$container_id, server PID=$server_pid" >&2
    container_env="$(docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "$container_id")"
    model="$(awk -F= '$1 == "WHISPER_MODEL" {print substr($0, index($0, "=") + 1)}' <<<"$container_env")"
    whisper_threads="$(awk -F= '$1 == "WHISPER_THREADS" {print substr($0, index($0, "=") + 1)}' <<<"$container_env")"
    queue_size="$(awk -F= '$1 == "QUEUE_SIZE" {print substr($0, index($0, "=") + 1)}' <<<"$container_env")"
    if [[ "$benchmark_mode" == "gpu" ]]; then
      gpu="$(docker exec "$container_id" nvidia-smi --query-gpu=name,memory.total --format=csv,noheader 2>/dev/null | paste -sd '; ' - || true)"
      if [[ -z "$gpu" ]]; then
        echo "CUDA GPU is not available inside voice-api; configure NVIDIA Container Toolkit before benchmarking" >&2
        exit 1
      fi
    fi

    report="$(RUN_ALL=0 WORKERS="$worker_count" MEMORY_PID='' MEMORY_CONTAINER="$container_id" AUDIO_DIR="$audio_dir" API_URL="$api_url" "$script_path")"
    printf '%s\n' "$report" >&2
    total_time="$(awk -F ': ' '/^total time:/ {print $2}' <<<"$report")"
    avg_latency="$(awk -F ': ' '/^avg job latency:/ {print $2}' <<<"$report")"
    peak_memory="$(awk -F ': ' '/^peak memory:/ {print $2}' <<<"$report")"
    printf '| %s | %s | %s | %s |\n' "$worker_count" "$total_time" "$avg_latency" "$peak_memory" >>"$table_file"
  done
  cpu="$(LC_ALL=C lscpu 2>/dev/null | awk -F: '/Model name:/ {sub(/^[[:space:]]+/, "", $2); print $2; exit}')"
  cpu="${cpu:-$(uname -m)}"
  ram="$(free -h 2>/dev/null | awk '/^Mem:/ {print $2; exit}')"
  ram="${ram:-unknown}"
  {
    printf '# Voice-recognition benchmark results\n\n'
    printf 'Generated: %s\n\n' "$(date --iso-8601=seconds)"
    printf '## Test conditions\n\n'
    printf -- '- Mode: %s\n' "$benchmark_mode"
    printf -- '- Dataset: %s WAV files from `%s`\n' "$dataset_count" "$audio_dir"
    printf -- '- Model: `%s`\n' "${model:-unknown}"
    printf -- '- Whisper threads per worker: `%s`\n' "${whisper_threads:-unknown}"
    printf -- '- Queue size: `%s`\n' "${queue_size:-unknown}"
    printf -- '- OS: `%s`\n' "$(uname -srmo)"
    printf -- '- CPU: %s\n' "$cpu"
    printf -- '- RAM: %s\n' "$ram"
    printf -- '- GPU: %s\n\n' "$gpu"
    printf '## Measurements\n\n'
    cat "$table_file"
    printf '\nLatency is measured from `startedAt` to `finishedAt`; peak memory covers the Docker container, including `whisper-cli`.\n'
  } >"$results_file"
  cat "$table_file"
  echo "results saved to $results_file" >&2
}

if [[ "$run_all" == "1" ]]; then
  run_all_workers
  exit 0
fi

command -v curl >/dev/null
command -v jq >/dev/null
command -v date >/dev/null

mapfile -d '' files < <(find "$audio_dir" -maxdepth 1 -type f -iname '*.wav' -print0 | sort -z)
if ((${#files[@]} == 0)); then
  echo "no .wav files in $audio_dir" >&2
  exit 1
fi

now_ms() { date +%s%3N; }
iso_ms() { date -d "$1" +%s%3N; }

peak_file="$(mktemp)"
sampler_pid=""
cleanup() {
  [[ -n "$sampler_pid" ]] && kill "$sampler_pid" 2>/dev/null || true
  rm -f "$peak_file"
}
trap cleanup EXIT

start_pid_sampler() {
  [[ -z "$memory_pid" ]] && return
  (
    peak=0
    while kill -0 "$memory_pid" 2>/dev/null; do
      rss="$(ps -o rss= -p "$memory_pid" | tr -d ' ')"
      if [[ "$rss" =~ ^[0-9]+$ ]] && (( rss > peak )); then
        peak="$rss"
      fi
      printf '%s\n' "$peak" >"$peak_file"
      sleep 1
    done
  ) &
  sampler_pid="$!"
}

memory_to_kib() {
  # docker stats выводит значения наподобие "315.5MiB / 7.7GiB".
  awk '{
    value = $1
    number = value; sub(/[[:alpha:]]+$/, "", number)
    unit = value; sub(/^[0-9.]+/, "", unit)
    factor = 1
    if (unit == "B") factor = 1 / 1024
    else if (unit == "KiB") factor = 1
    else if (unit == "MiB") factor = 1024
    else if (unit == "GiB") factor = 1024 * 1024
    else if (unit == "TiB") factor = 1024 * 1024 * 1024
    printf "%.0f\n", number * factor
  }'
}

start_container_sampler() {
  [[ -z "$memory_container" ]] && return
  (
    peak=0
    while true; do
      usage="$(docker stats --no-stream --format '{{.MemUsage}}' "$memory_container" 2>/dev/null || true)"
      [[ -z "$usage" ]] && break
      current="$(memory_to_kib <<<"$usage")"
      if [[ "$current" =~ ^[0-9]+$ ]] && (( current > peak )); then
        peak="$current"
      fi
      printf '%s\n' "$peak" >"$peak_file"
      sleep 1
    done
  ) &
  sampler_pid="$!"
}

start_pid_sampler
start_container_sampler

started="$(now_ms)"
ids=()
for audio in "${files[@]}"; do
  response="$(curl --fail-with-body --silent --show-error -F "file=@$audio" "$api_url/audio")"
  job_id="$(jq -r '.jobId // empty' <<<"$response")"
  if [[ -z "$job_id" ]]; then
    echo "server did not return jobId for $audio: $response" >&2
    exit 1
  fi
  ids+=("$job_id")
done

latency_sum=0
for job_id in "${ids[@]}"; do
  while true; do
    job="$(curl --fail --silent --show-error "$api_url/jobs/$job_id")"
    status="$(jq -r '.status' <<<"$job")"
    case "$status" in
      completed)
        processing_started="$(jq -r '.startedAt' <<<"$job")"
        processing_finished="$(jq -r '.finishedAt' <<<"$job")"
        latency_sum=$((latency_sum + $(iso_ms "$processing_finished") - $(iso_ms "$processing_started")))
        break
        ;;
      failed)
        echo "job $job_id failed: $(jq -r '.error // "unknown error"' <<<"$job")" >&2
        exit 1
        ;;
    esac
    sleep "$poll_seconds"
  done
done
finished="$(now_ms)"

count="${#ids[@]}"
total_ms=$((finished - started))
avg_latency_ms=$((latency_sum / count))
throughput_milli=$((count * 1000000 / total_ms))
memory="not measured"
if [[ -n "$sampler_pid" ]]; then
  kill "$sampler_pid" 2>/dev/null || true
  wait "$sampler_pid" || true
  sampler_pid=""
  if [[ -n "$memory_container" ]]; then
    memory="$(<"$peak_file") KiB (Docker container, including whisper-cli)"
  else
    memory="$(<"$peak_file") KiB RSS (server process only)"
  fi
fi

printf 'workers: %s\nfiles: %s\ntotal time: %s ms\navg job latency: %s ms\nthroughput: %s.%03s jobs/s\npeak memory: %s\n' \
  "$workers" "$count" "$total_ms" "$avg_latency_ms" \
  "$((throughput_milli / 1000))" "$((throughput_milli % 1000))" "$memory"
