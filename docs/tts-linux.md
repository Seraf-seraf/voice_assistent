# Локальная речь в Linux

Целевой runtime этой настройки — Linux amd64/glibc с CGO. Обычная сборка TTS
требует Go toolchain проекта, C compiler, `pkg-config` и ALSA development files
(`libasound2-dev`). Приложение не устанавливает системные пакеты и не загружает
голос во время запуска. Если `git` или `git-lfs` отсутствует, установите их
отдельным системным способом до подготовки модели.

Выбран технический стартовый голос VITS `ru_RU-ruslan-medium`, подготовленный
для sherpa-onnx. Один ответ разбивается на короткие фразы, каждая фраза сначала
синтезируется целиком, затем проигрывается. Текст продолжает поступать во время
проигрывания, но синтез следующей фразы ждёт окончания предыдущей; между
фразами возможны паузы. Первый голос не является обещанием выбранного тембра или
качества произношения. TTS включается, когда задан непустой `tts.model_dir`;
при ошибке модели или устройства приложение возвращает ошибку и не переключается
молча на text-only.

## Однократная подготовка модели

Для нового каталога проверьте зависимости:

```bash
git --version
git lfs version
```

Если любой команды нет, остановитесь и установите соответствующую зависимость.
Не запускайте файлы Python или shell из репозитория модели.

Закреплённая ревизия модели:

Проверьте, что каталог `VOICE_DIR` ещё не существует. Если он уже есть,
пропустите clone и сначала выполните проверки существующего каталога ниже.

```bash
VOICE_ROOT="${XDG_DATA_HOME:-$HOME/.local/share}/voice-assistant/tts"
VOICE_DIR="$VOICE_ROOT/ruslan-22c2268"
mkdir -p "$VOICE_ROOT"
GIT_LFS_SKIP_SMUDGE=1 git clone --no-checkout \
  https://huggingface.co/csukuangfj/vits-piper-ru_RU-ruslan-medium "$VOICE_DIR"
git -C "$VOICE_DIR" checkout --detach 22c226825c24319e30cbf8a3844a240b9f6b1951
git -C "$VOICE_DIR" lfs pull
git -C "$VOICE_DIR" lfs fsck
sha256sum "$VOICE_DIR/ru_RU-ruslan-medium.onnx"
```

Ожидаемый SHA-256 файла `ru_RU-ruslan-medium.onnx`:

```text
43d3e034b04abe67c9ceab09a062617260e71804f55779cc68979a7b3e434064
```

Для уже существующего `VOICE_DIR` не выполняйте `clone`, `reset`, `clean` или
повторную загрузку поверх неизвестного состояния. Сначала осмотрите репозиторий
и файлы:

```bash
git -C "$VOICE_DIR" rev-parse HEAD
git -C "$VOICE_DIR" status --short
git -C "$VOICE_DIR" lfs ls-files
git -C "$VOICE_DIR" lfs fsck
test -s "$VOICE_DIR/ru_RU-ruslan-medium.onnx"
test -s "$VOICE_DIR/tokens.txt"
test -d "$VOICE_DIR/espeak-ng-data"
sha256sum "$VOICE_DIR/ru_RU-ruslan-medium.onnx"
```

Сверьте HEAD с `22c226825c24319e30cbf8a3844a240b9f6b1951`. При другом HEAD,
локальных изменениях, отсутствующих materialized LFS файлах или несовпадающем
hash сначала разберите состояние вручную. Не стирайте чужие изменения.

В `MODEL_CARD` для набора данных указана лицензия CC BY-NC-SA 4.0. Сохраняйте
`MODEL_CARD` и third-party notices рядом с внешними assets; не считайте лицензию
модели или native bundle единой MIT/Apache лицензией. Подключение предназначено
для локального прототипа, а не для коммерческого распространения без отдельной
проверки условий.

## Конфигурация

Укажите абсолютный путь к каталогу закреплённой ревизии в своём существующем
конфиге:

```yaml
tts:
  model_dir: /absolute/path/to/ruslan-22c2268
  threads: 2
  timeout: 5m

audio:
  output_device: default
```

`audio.output_device` здесь — имя ALSA PCM. Пустая строка выбирает ALSA `default`;
явно заданное имя, например `pulse`, передаётся backend как есть. Устройство
не подменяется при ошибке. Для WSL нужен работающий Linux audio server и
установленный ALSA Pulse plugin; задайте `pulse` явно, если это ваш endpoint.
Используйте ALSA endpoint из того же пользовательского Linux-сеанса, в котором
работает assistant. Не используйте UNC-путь или WASAPI в Linux и не создавайте
PulseAudio/PipeWire daemon от root. Приложение не меняет системный default route.

Локальная GGUF-модель LLM остаётся на прежнем Linux-пути:

```text
/home/seraf/.lmstudio/models/lmstudio-community/Qwen3.5-0.8B-GGUF/Qwen3.5-0.8B-Q4_K_M.gguf
```

## Native и слышимая проверка

Сборка обычных unit tests не скачивает модель и не проигрывает звук. Для native
synthesis укажите выбранные assets и запустите отдельный integration target:

```bash
ASSISTANT_TTS_MODEL_DIR="$VOICE_DIR" make test-tts-integration
```

Этот тест подтверждает синтез pinned model, но не подтверждает звук устройства.
Для playback выберите наушники или низкую системную громкость, затем укажите
реальный ALSA endpoint. Значение `null` проверяет только backend без слышимого
выхода:

```bash
ASSISTANT_PLAYBACK_TEST_DEVICE="hw:0,0" make test-playback-integration
```

Подтвердите звук вручную на выбранном endpoint. Перед использованием микрофона
проверьте сценарий в наушниках. Подтверждённая новая речь отменяет текущий ответ
без остановки приложения; старый handler должен завершить cleanup до следующего
ответа. Команда «стоп» сначала прерывает ответ по событию речи, затем поглощается
router без новой генерации. В историю остаётся непрерывный префикс фраз с успешным
возвратом Player.Play после drain. Это подтверждение ALSA backend, а не точная
оценка физически услышанных слов. Echo cancellation отсутствует, поэтому микрофон
может повторно захватить голос из колонок; полноценный global PTT не входит в этот
этап.

Проверки interrupt и общие gates:

```bash
go test ./internal/assistant ./internal/dialogue -count=1
go test ./internal/llm/llamacpp ./internal/tts/sherpa ./internal/audio/output/alsa ./cmd/assistant -count=1
go test -race ./internal/assistant -count=10
make test
make test-race
make lint
make build
make test-llamacpp-integration
make test-tts-integration
make test-playback-integration
```

Три последних команды требуют уже настроенных GGUF/native-библиотек, TTS assets и
выбранного ALSA endpoint. Они не загружают модели автоматически. Playback gate
воспроизводит тихий сигнал на выбранном устройстве.

При ручной проверке полного assistant используйте запрос из двух-трёх коротких
предложений. Зафиксируйте наблюдённые моменты первой видимой текстовой дельты,
начала первой фразы и окончания playback; fake tests не подтверждают latency
на реальном устройстве. Проверьте отсутствие повтора/потери фраз и что следующая
реплика использует завершённую историю. Ctrl+C во время inference может ждать
возврата текущей native ONNX операции; pending playback отменяется, но жёсткая
preemption C-вызова не обещается.

## Linux package

В Linux/amd64 окружении с CGO, `readelf` и `ldd` выполните:

```bash
make package-linux-tts
```

Пакет появляется в `dist/voice-assistant-linux-amd64`. Он включает assistant,
закреплённые sherpa/ONNX shared libraries из Go module cache, launcher и LICENSE
Go-модуля с перечнем native-файлов. Проверка `ldd` убеждается, что sherpa и
ONNX Runtime разрешаются из `package/lib`. Другие лицензии native-зависимостей
этот LICENSE не заменяет. Пакет не включает голос, GGUF, локальный config или
системный ALSA runtime. Это не single-file бинарник.

Для запуска передайте свой конфиг:

```bash
dist/voice-assistant-linux-amd64/run-assistant -config /absolute/path/to/assistant.yaml
```
