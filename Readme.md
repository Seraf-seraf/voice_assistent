# Voice Assistant

Локальный голосовой ассистент на Go. Приложение получает звук с microphone,
сегментирует речь через VAD и отправляет завершённые utterance в STT/Whisper.
Результат пока остаётся raw transcription. Normalization, router и dialogue
ещё не подключены.

## Требования

- Go 1.25 или новее;
- CGO и GCC-compatible C compiler для сборки аудиовхода через miniaudio;
- CGO для глобальной hold-PTT клавиши в Windows;
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

Тракт обработки: microphone → VAD → utterance → STT/Whisper → raw
transcription. Normalization/router/dialogue ещё не подключены.
