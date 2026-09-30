# Voice Assistant

Локальный голосовой ассистент на Go для Linux amd64/glibc и Windows amd64. Приложение
получает звук с microphone, сегментирует речь через VAD, распознаёт её в
STT/Whisper, нормализует текст и передаёт его в router и локальную LLM. Ответ
выводится в stdout по мере генерации; при включённом TTS он также озвучивается
локальным голосом короткими законченными фразами. Для TTS Linux использует
PulseAudio, Windows — WASAPI; настройка описана в
[docs/tts-linux.md](docs/tts-linux.md) и [docs/tts-windows.md](docs/tts-windows.md).
Между фразами возможны паузы.

Новая подтверждённая речь прерывает текущий ответ и заменяет ожидающие старые
запросы; процесс и модели остаются запущены. Команда «стоп» после распознавания
не начинает новый ответ.

## Требования

- Go toolchain из `go.mod`;
- Linux amd64/glibc или Windows amd64, CGO и C compiler;
- Linux: runtime `libpulse.so.0`; для Windows cross-build из Linux — MinGW-w64;
- локальная GGUF-модель и native llama.cpp библиотеки для целевой ОС;
- Docker с NVIDIA Container Toolkit для Whisper;
- реальный PulseAudio endpoint в Linux или WASAPI endpoint в Windows для проверки слышимого вывода. TTS включается непустым `tts.model_dir`.

## Настройка

Скопируйте пример конфигурации:

```bash
cp config/assistant.example.yaml config/assistant.yaml
```

STT API-токен не записывается в YAML. При необходимости используйте переменную
`ASSISTANT_STT_API_KEY`. Поддерживаемые overrides перечислены в
`internal/bootstrap/config/config.go`.

## Слои кода

- `internal/service` содержит высокоуровневые контракты и независимые правила:
  аудиоданные, диалог, маршрутизацию, LLM/STT/TTS/VAD и диагностику.
- `internal/platform` содержит низкоуровневые реализации: аудиоустройства,
  HTTP-клиент Whisper, sherpa-onnx, llama.cpp, WebRTC VAD и журнал.
- `internal/app` связывает сервисы в сценарии обработки речи и ответа.
- `internal/bootstrap` загружает конфигурацию, выбирает реализации и собирает
  приложение. `cmd/assistant` остаётся тонкой точкой входа.

Зависимости направлены внутрь: `app` использует `service`, `platform`
реализует контракты `service`, а `bootstrap` связывает эти части.

## Команды

Linux использует корневой `Makefile`, Windows — `Makefile.windows`. Цель `test`
запускает все Go-тесты и собирает приложение, поэтому отдельная команда сборки
не нужна:

```bash
# Linux amd64
make test
make run
make package

# Windows amd64
make -f Makefile.windows test
make -f Makefile.windows run
make -f Makefile.windows package
```

`up` и `down` доступны в обоих файлах для управления Docker-сервисами: в Linux
это `make up` и `make down`; в Windows —
`make -f Makefile.windows up` и `make -f Makefile.windows down`.
Native-проверки с моделью и аудиоустройством описаны в инструкциях TTS.

Тракт обработки: microphone → VAD → STT/Whisper → normalization → router →
локальная LLM → потоковый текст в stdout и, при включённом TTS, последовательная
озвучка коротких фраз через платформенный аудиовыход. STT продолжает обращаться
к Whisper через собственный HTTP adapter. Команда reset history очищает историю.
Ответ генерируется последовательно в STT worker. Новый ответ не начинает работу,
пока предыдущий handler и очистка playback не завершились. В историю прерванного
голосового ответа попадает только непрерывный префикс фраз с успешным завершением
`Player.Play`; это подтверждение аудиобэкенда, а не гарантия, что человек услышал
звук. Частично проигранная фраза и текстовый вывод не считаются произнесёнными.
Полный ответ записывается только после успешного `Complete`.

## LLM port

`llm.Generator` остаётся нейтральным портом. Production adapter использует
YZMA v1.28.0 и llama.cpp v0.5.0 для локальной модели Qwen3.5-0.8B GGUF.
Локальные веса и native DLL/SO не хранятся в Git. Настройка, установка
библиотек и запуск проверок описаны в [docs/local-llm.md](docs/local-llm.md).

Озвучка пофразовая; между синтезом и воспроизведением возможны паузы. Это не
беззазорный audio-token streaming. При новой речи текущая генерация отменяется,
а следующий запрос ожидает завершения старого обработчика. Для проверки barge-in
используйте наушники: подавления акустического эха нет. Global PTT hotkey не
подключён.
