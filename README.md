# Voice recognition API

HTTP-сервис принимает WAV-файлы и распознаёт речь локальным `whisper.cpp`.
Загрузка создаёт задание сразу: HTTP-обработчик не ожидает выполнения модели.

## Запуск

Нужны Docker, файл модели в
`./models` и исходники `whisper.cpp` в
`./third_party/whisper.cpp`. По умолчанию указан файл модели
`ggml-large-v3-turbo-q8_0.bin`.

### CPU

Обычный запуск не требует GPU: Docker по умолчанию собирает CPU-версию
`whisper.cpp`.

```bash
cp .env.example .env
docker compose up --build
```

### NVIDIA GPU

Для CUDA нужны NVIDIA GPU с драйвером и NVIDIA Container Toolkit. GPU-вариант
собирает `whisper.cpp` с `GGML_CUDA=ON` и запрашивает одну видеокарту через
отдельный Compose-файл. В `.env` задаётся `CUDA_ARCHITECTURES`; `89` — RTX
4060. Для другой NVIDIA GPU укажите её CUDA compute capability до сборки.
Проверьте, что Docker видит видеокарту:

```bash
docker run --rm --runtime=nvidia --gpus all ubuntu nvidia-smi
```

Если команда не выводит GPU, используйте CPU-режим или настройте NVIDIA
Container Toolkit и перезапустите Docker daemon согласно документации NVIDIA.

Скачайте исходники через браузер со страницы
https://github.com/ggml-org/whisper.cpp/archive/refs/heads/master.zip и
распакуйте так, чтобы существовал файл `third_party/whisper.cpp/CMakeLists.txt`.
Например, после распаковки папку `whisper.cpp-master` нужно переименовать в
`whisper.cpp`.

```bash
docker compose -f docker-compose.yml -f docker-compose.gpu.yml up --build
```

Сервис доступен на `http://localhost:18080`. Вариант без Docker требует
`whisper-cli`, PostgreSQL и те же переменные окружения из `.env.example`.

Интерактивная документация Swagger UI доступна по
`http://localhost:18080/swagger/`; исходная OpenAPI-спецификация — по
`http://localhost:18080/openapi.yaml`.

## API

```bash
# Создать задание (ответ 201: {"jobId":"..."})
curl -F 'file=@sample.wav' 'http://localhost:18080/audio?language=ru'

curl http://localhost:18080/jobs
curl http://localhost:18080/jobs/<job-id>
curl http://localhost:18080/jobs/<job-id>/transcript
curl --get --data-urlencode 'q=искомая фраза' http://localhost:18080/search
```

Поддерживается только `.wav`; размер WAV-файла ограничен `MAX_UPLOAD_MB` и
записывается на диск потоково. `GET /jobs/{id}/transcript` вернёт `409`, пока
задание не завершено. Поиск регистронезависимый, только по завершённым
транскриптам; каждый результат содержит `jobId`, `fileName` и фрагмент текста.

## Очередь, восстановление и остановка

`WORKERS` задаёт постоянное число worker'ов, `QUEUE_SIZE` — ёмкость очереди.
Когда очередь заполнена, `POST /audio` отвечает `503 Service Unavailable` и
не оставляет в БД или на диске отклонённое задание. Это ограничивает потребление
памяти при входящем потоке, превышающем скорость модели.

Задания и транскрипты хранятся в PostgreSQL, исходные WAV — в `UPLOAD_DIR`.
При старте все задания со статусом `queued` или `processing` возвращаются в
`queued` и снова подаются worker'ам. Поэтому завершённые результаты доступны
после рестарта, а незавершённые распознаются повторно.

По `SIGINT`/`SIGTERM` сервис перестаёт принимать новые задания, останавливает
HTTP-сервер и дожидается опустошения очереди до `SHUTDOWN_TIMEOUT`. Затем
контекст отменяется: активный процесс `whisper-cli` завершается, а его задание
возвращается в `queued` для следующего запуска. Отмена зависит от ОС: она
реализована через `exec.CommandContext`, то есть посылает сигнал локальному
процессу модели.

## Проверка

```bash
GOCACHE=/tmp/voice-api-go-build go test ./...
GOCACHE=/tmp/voice-api-go-build go test -race ./...
GOCACHE=/tmp/voice-api-go-build go vet ./...
```

## Эксперимент с производительностью

Подготовьте один и тот же набор из десяти WAV длительностью 30–60 секунд.
Автоматический режим сам перезапускает Docker-сервис для `WORKERS=1`, `2` и
`4`, ждёт `/healthz`, выводит PID процесса сервера, замеряет пиковую память
контейнера (включая дочерний `whisper-cli`) и записывает готовый отчёт в
`benchmark-result-cpu.md` или `benchmark-result-gpu.md`. Отчёт содержит Markdown-таблицу, модель, параметры
сервиса, размер набора и характеристики ОС, CPU, RAM и GPU (если обнаружен):

```bash
RUN_ALL=1 AUDIO_DIR=./benchmark-audio ./scripts/benchmark.sh
# NVIDIA GPU
RUN_ALL=1 BENCHMARK_MODE=gpu AUDIO_DIR=./benchmark-audio ./scripts/benchmark.sh
```

Для этого режима нужны доступ к Docker daemon, `bash`, `curl`, `jq` и GNU
`date`. Образ выбранного режима собирается один раз перед всей серией, а контейнер
`voice-api` пересоздаётся для каждого значения `WORKERS`. Данные PostgreSQL и
загруженные аудио сохраняются. Каждый прогон отправляет одинаковый набор файлов
как новые задания, поэтому сохранённые результаты предыдущих прогонов на
измерения не влияют. `BENCHMARK_MODE` принимает `cpu` (по умолчанию) или `gpu`.
Чтобы выбрать другой путь для отчёта, передайте
`BENCHMARK_RESULTS_FILE=/путь/к/отчёту.md`.

Одиночный прогон остаётся доступен, если сервис уже запущен. Размер очереди
должен быть не меньше числа файлов:

```bash
WORKERS=1 QUEUE_SIZE=10 docker compose up --build
# в другом терминале
WORKERS=1 AUDIO_DIR=./benchmark-audio ./scripts/benchmark.sh
```

Скрипт отправляет все файлы, ждёт их завершения и выводит total time, среднюю
latency (`processing` → `completed`) и throughput. В одиночном режиме для
памяти процесса API передайте `MEMORY_PID=$(pgrep -f voice-recognition)`.

### Результаты измерений

Стенд: Manjaro Linux 6.12.108, AMD Ryzen 5 5600 (6 ядер / 12 логических
потоков), 31 GiB RAM, NVIDIA GeForce RTX 4060 с 8 GiB VRAM. Набор данных — 10
WAV-файлов по 60 секунд из `test-audio/wav`. Во всех прогонах использовались
`ggml-large-v3-turbo-q8_0.bin`, `WHISPER_THREADS=6` и `QUEUE_SIZE=50`.

В CPU-серии `whisper.cpp` был собран без CUDA:

| Workers | Total time | Avg job latency | Throughput | Peak container RAM |
| ---: | ---: | ---: | ---: | ---: |
| 1 | 181.895 s | 18.181 s | 0.055 job/s | 1.14 GiB |
| 2 | 176.097 s | 35.190 s | 0.057 job/s | 2.26 GiB |
| 4 | 557.668 s | 217.503 s | 0.018 job/s | 4.50 GiB |

В GPU-серии `whisper.cpp` был собран с `GGML_CUDA=ON` для CUDA architecture
89 (RTX 4060):

| Workers | Total time | Avg job latency | Throughput | Peak container RAM |
| ---: | ---: | ---: | ---: | ---: |
| 1 | 27.087 s | 2.683 s | 0.369 job/s | 0.32 GiB |
| 2 | 16.257 s | 3.216 s | 0.615 job/s | 0.63 GiB |
| 4 | 16.197 s | 5.734 s | 0.617 job/s | 1.14 GiB |

Latency — время одного задания от `startedAt` до `finishedAt`; throughput —
количество завершённых заданий за секунду. `Peak container RAM` измерен через
`docker stats`; это оперативная память контейнера, а не пиковое потребление
VRAM.

### Выводы

В CPU-режиме увеличение `WORKERS` не принесло заметного роста throughput: два
процесса уже занимали до 12 потоков (`2 × WHISPER_THREADS=6`), а четыре процесса
создавали до 24 конкурирующих потоков и четыре копии модели. Поэтому при
`WORKERS=4` выросли latency и RAM, а throughput упал примерно втрое относительно
одного worker.

CUDA существенно улучшила результат: при `WORKERS=2` набор обработан за 16.257
секунды вместо 176.097 секунды в CPU-прогоне с тем же числом workers. Переход с
одного на два GPU-worker увеличил throughput на 67%. Переход с двух на четыре
не дал практически никакого выигрыша по общему времени (16.257 против 16.197
секунды), но увеличил latency на 78% и RAM на 81%. Единственная RTX 4060 к двум
одновременным процессам уже насыщена: дополнительные процессы конкурируют за
вычислительные ресурсы и видеопамять. Рекомендуемая конфигурация для этого
стенда — `WORKERS=2`.
