# Voice Assistant

Локальный голосовой ассистент на Go. Приложение запускает microphone/VAD runtime
и обрабатывает события `SpeechStarted`/`SpeechEnded`. STT-клиент создаётся, но
транскрипция ещё не подключена. Dialogue/control остаются каркасом.

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

Приложение запускает microphone/VAD runtime и обрабатывает
`SpeechStarted`/`SpeechEnded`. STT-клиент создан, но транскрипция ещё не
подключена: аудио не отправляется в STT.
