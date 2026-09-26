# Voice Assistant

Локальный голосовой ассистент на Go. Приложение получает звук с microphone,
сегментирует речь через VAD, распознаёт её в STT/Whisper, нормализует текст и
передаёт его в router. Команда очистки истории подключена; обычный запрос
доходит до query boundary без генератора ответа.

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

Тракт обработки: microphone → VAD → STT/Whisper → normalization → router →
query boundary. Reset history подключён. Dialogue turn пока не начинается,
генератора ответа нет. Режим PTT не включает global hotkey в runtime.

## LLM port

Добавлены нейтральный интерфейс `llm.Generator` и прикладной `Responder`,
который управляет dialogue turn и собирает полный текстовый ответ. Их поведение
проверяется с тестовым fake, но production consumer пока не подключён. Модели и
адаптера генерации нет, поэтому приложение пока не генерирует ответ.

Сборка текста в `Responder` не является streaming-выводом и не воспроизводит
речь.
