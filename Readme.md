# Voice Assistant

Локальный голосовой ассистент на Go. Приложение запускается на Windows и управляет
аудиотрактом и внешними сервисами Whisper, LM Studio и Piper/OpenedAI Speech.

## Требования

- Go 1.25 или новее;
- CGO и GCC-compatible C compiler для сборки аудиовхода через miniaudio;
- CGO для глобальной hold-PTT клавиши в Windows;
- Docker с NVIDIA Container Toolkit для Whisper;
- LM Studio с запущенным OpenAI-compatible API;
- Windows-аудиоустройства, доступные через WASAPI.

## Настройка

Скопируйте пример конфигурации и укажите точный идентификатор модели из LM Studio:

```bash
cp config/assistant.example.yaml config/assistant.yaml
```

API-токены не записываются в YAML. При необходимости используйте переменные
`ASSISTANT_STT_API_KEY`, `ASSISTANT_LLM_API_KEY` и `ASSISTANT_TTS_API_KEY`.
Остальные поддерживаемые overrides перечислены в `internal/config/config.go`.

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
make tts           # отдельный запуск контейнера OpenedAI Speech
make webui         # запуск Open WebUI
```

Сейчас реализован только проверяемый каркас конфигурации и логирования. Аудио и
внешние клиенты добавляются отдельными согласуемыми этапами.
