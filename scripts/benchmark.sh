#!/usr/bin/env bash
set -euo pipefail

# Sends one fixed WAV dataset to a running API and reports STT metrics.
# Required tools: bash, curl, jq, GNU date. Optional: ps (for MEMORY_PID).

api_url="${API_URL:-http://localhost:18080}"
audio_dir="${AUDIO_DIR:?set AUDIO_DIR to a directory containing WAV files}"
workers="${WORKERS:-unknown}"
poll_seconds="${POLL_SECONDS:-0.2}"
memory_pid="${MEMORY_PID:-}"

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

if [[ -n "$memory_pid" ]]; then
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
fi

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
  memory="$(<"$peak_file") KiB RSS (server process only)"
fi

printf 'workers: %s\nfiles: %s\ntotal time: %s ms\navg job latency: %s ms\nthroughput: %s.%03s jobs/s\npeak memory: %s\n' \
  "$workers" "$count" "$total_ms" "$avg_latency_ms" \
  "$((throughput_milli / 1000))" "$((throughput_milli % 1000))" "$memory"
