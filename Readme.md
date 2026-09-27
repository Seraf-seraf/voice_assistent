# Voice Assistant

Локальный голосовой ассистент на Go для Linux amd64/glibc. Приложение получает
звук с microphone, сегментирует речь через VAD, распознаёт её в STT/Whisper,
нормализует текст и передаёт его в router и локальную LLM. Ответ выводится в
stdout по мере генерации; при включённом TTS он также озвучивается локальным
голосом короткими законченными фразами. Подготовка модели и Linux ALSA описаны в
[docs/tts-linux.md](docs/tts-linux.md). Этап `[interrupt]` остаётся следующим:
голосовая команда пока не останавливает генерацию и воспроизведение.

## Требования

- Go toolchain из `go.mod`;
- Linux amd64/glibc, CGO и GCC-compatible C compiler;
- ALSA development files (`libasound2-dev` и `pkg-config`) для сборки Linux TTS;
- локальная GGUF-модель Qwen3.5-0.8B и native llama.cpp библиотеки для Linux;
- Docker с NVIDIA Container Toolkit для Whisper;
- реальный Linux ALSA endpoint для проверки слышимого вывода. TTS по умолчанию выключен.

## Настройка

Скопируйте пример конфигурации:

```bash
cp config/assistant.example.yaml config/assistant.yaml
```

STT API-токен не записывается в YAML. При необходимости используйте переменную
`ASSISTANT_STT_API_KEY`. Поддерживаемые overrides перечислены в `internal/config/config.go`.

## Команды

```bash
make test          # unit-тесты
make test-race     # тесты с race detector
make lint          # gofmt check и go vet
make build         # сборка для текущей ОС
make build-windows # сборка Windows executable
make package-linux-tts # пакет Linux с native sherpa shared libraries
make run           # запуск с config/assistant.yaml
make up            # запуск Docker-сервисов
make down          # остановка Docker-сервисов
```

Тракт обработки: microphone → VAD → STT/Whisper → normalization → router →
локальная LLM → потоковый текст в stdout и, при включённом TTS, последовательная
озвучка коротких фраз. STT продолжает обращаться к Whisper
через собственный HTTP adapter. Команда reset history очищает историю. Ответ
генерируется последовательно в STT worker.

## LLM port

`llm.Generator` остаётся нейтральным портом. Production adapter использует
YZMA v1.28.0 и llama.cpp v0.5.0 для локальной модели Qwen3.5-0.8B GGUF.
Локальные веса и native DLL/SO не хранятся в Git. Настройка, установка
библиотек и запуск проверок описаны в [docs/local-llm.md](docs/local-llm.md).

`Responder` сохраняет полный ответ в истории только после окончания всех
озвученных фраз и успешного завершения вывода. Озвучка пофразовая; между
синтезом и воспроизведением возможны паузы. Это не беззазорный audio-token
streaming. PTT hotkey не подключён; `[interrupt]` остаётся следующим этапом.
