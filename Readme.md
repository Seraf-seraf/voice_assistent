# Voice Assistant

Локальный голосовой ассистент на Go. Приложение получает звук с microphone,
сегментирует речь через VAD, распознаёт её в STT/Whisper, нормализует текст и
передаёт его в router. Команда очистки истории подключена; обычный запрос
доходит до query boundary без генератора ответа.

## Требования

- Go 1.27.1 (toolchain задаётся в `go.mod`; минимальная версия модуля — Go 1.26.0);
- CGO и GCC-compatible C compiler для сборки аудиовхода через miniaudio;
- CGO для глобальной hold-PTT клавиши в Windows;
- локальная GGUF-модель Qwen3.5-0.8B и native llama.cpp библиотеки для целевой ОС;
- Docker с NVIDIA Container Toolkit для Whisper;
- Windows-аудиоустройства, доступные через WASAPI.

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
make run           # запуск с config/assistant.yaml
make up            # запуск Docker-сервисов
make down          # остановка Docker-сервисов
```

Тракт обработки: microphone → VAD → STT/Whisper → normalization → router →
локальная LLM → полный текстовый ответ в stdout. STT продолжает обращаться к
Whisper через собственный HTTP adapter. Команда reset history очищает историю.
Ответ генерируется последовательно в STT worker.

## LLM port

`llm.Generator` остаётся нейтральным портом. Production adapter использует
YZMA v1.28.0 и llama.cpp v0.5.0 для локальной модели Qwen3.5-0.8B GGUF.
Локальные веса и native DLL/SO не хранятся в Git. Настройка, установка
библиотек и запуск проверок описаны в [docs/local-llm.md](docs/local-llm.md).

`Responder` собирает полный ответ перед выводом. Streaming и TTS не реализованы.
PTT hotkey не подключён; голосовая команда «стоп» не прерывает текущую
генерацию.
