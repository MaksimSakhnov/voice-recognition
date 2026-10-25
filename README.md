# Voice recognition API

HTTP-сервис принимает WAV-файлы и распознаёт речь локальным `whisper.cpp`.
Загрузка создаёт задание сразу: HTTP-обработчик не ожидает выполнения модели.

## Запуск

Нужны Docker, файл модели в `./models` и исходники `whisper.cpp` в
`./third_party/whisper.cpp`. По умолчанию указан файл модели
`ggml-large-v3-turbo-q8_0.bin`.

Скачайте исходники через браузер со страницы
https://github.com/ggml-org/whisper.cpp/archive/refs/heads/master.zip и
распакуйте так, чтобы существовал файл `third_party/whisper.cpp/CMakeLists.txt`.
Например, после распаковки папку `whisper.cpp-master` нужно переименовать в
`whisper.cpp`.

```bash
cp .env.example .env
docker compose up --build
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

Поддерживается только `.wav`; тело запроса ограничено `MAX_UPLOAD_MB` и
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
Для каждого прогона перезапускайте сервис с новой величиной `WORKERS`; размер
очереди должен быть не меньше числа файлов:

```bash
WORKERS=1 QUEUE_SIZE=10 docker compose up --build
# в другом терминале
WORKERS=1 AUDIO_DIR=./benchmark-audio ./scripts/benchmark.sh
```

Повторите для `WORKERS=2` и `WORKERS=4`. Скрипт требует `bash`, `curl` и `jq`,
отправляет все файлы, ждёт их завершения и выводит total time, среднюю latency
(`processing` → `completed`) и throughput. Для памяти передайте PID процесса
сервиса: `MEMORY_PID=$(pgrep -f voice-recognition)`. В Docker для точного пика
вместе с дочерним `whisper-cli` удобнее наблюдать контейнер командой
`docker stats voice-api-voice-api-1` и записать пик из неё.

Заполните фактическими результатами именно своей машины (модель, параметры и
характеристики CPU/RAM тоже укажите):

| Workers | Total time | Avg job latency | Peak memory |
| ---: | ---: | ---: | ---: |
| 1 | — | — | — |
| 2 | — | — | — |
| 4 | — | — | — |

Latency измеряет время одного задания между `startedAt` и `finishedAt`;
throughput — число завершённых файлов за всё время прогона. В выводах сравните
рост throughput с изменением latency: несколько экземпляров модели конкурируют
за CPU/GPU и память, поэтому после некоторого числа worker'ов ускорение обычно
перестаёт быть линейным или становится отрицательным.
