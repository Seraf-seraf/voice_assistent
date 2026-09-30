# Локальная речь в Windows

Целевая среда — Windows amd64 с CGO и MinGW-w64. TTS использует локальный
sherpa-onnx v1.12.35 и выход WASAPI. При обычном запуске интернет и Python не
нужны. Приложение не загружает модель при старте; `tts.model_dir` задаёт путь к
полному комплекту голосовых assets. Отсутствие модели или выбранного endpoint
возвращает ошибку, автоматического перехода к выводу только текста нет.

Windows-вариант использует тот же VITS `ru_RU-ruslan-medium`, что и Linux.
Ответ разбивается на короткие фразы: каждая фраза синтезируется целиком, затем
воспроизводится. Пока звучит фраза, текст ответа продолжает поступать; синтез
следующей фразы начинается после окончания воспроизведения предыдущей. Между
фразами возможны паузы.

## Подготовка голоса

Требуются `git` и `git-lfs`. Для нового каталога запустите PowerShell:

```powershell
git --version
git lfs version
$VoiceRoot = Join-Path $env:LOCALAPPDATA 'voice-assistant\tts'
$VoiceDir = Join-Path $VoiceRoot 'ruslan-22c2268'
if (Test-Path $VoiceDir) { throw "Каталог уже существует: $VoiceDir" }
New-Item -ItemType Directory -Force $VoiceRoot | Out-Null
$env:GIT_LFS_SKIP_SMUDGE = '1'
git clone --no-checkout https://huggingface.co/csukuangfj/vits-piper-ru_RU-ruslan-medium $VoiceDir
git -C $VoiceDir checkout --detach 22c226825c24319e30cbf8a3844a240b9f6b1951
git -C $VoiceDir lfs pull
git -C $VoiceDir lfs fsck
Remove-Item Env:GIT_LFS_SKIP_SMUDGE
Get-FileHash (Join-Path $VoiceDir 'ru_RU-ruslan-medium.onnx') -Algorithm SHA256
```

Ожидаемый SHA-256 модели: `43d3e034b04abe67c9ceab09a062617260e71804f55779cc68979a7b3e434064`.
Каталог должен также содержать `tokens.txt`, полную папку `espeak-ng-data`,
`MODEL_CARD` и JSON-файл модели. Если каталог уже существует, сначала вручную
проверьте его Git HEAD, изменения, загруженные LFS-файлы и hash; не клонируйте
поверх и не сбрасывайте неизвестное состояние.

В `MODEL_CARD` указана лицензия набора данных CC BY-NC-SA 4.0. Сохраняйте её и
third-party notices с assets. Лицензия Go-модуля не покрывает модель или все
native DLL. Эта интеграция предназначена для локального прототипа.

## Конфигурация

В своём YAML задайте абсолютный путь к каталогу модели:

```yaml
tts:
  model_dir: 'C:\Users\Имя\AppData\Local\voice-assistant\tts\ruslan-22c2268'
  threads: 2
  timeout: 5m

audio:
  output_device: default
```

Пустой `audio.output_device` или `default` выбирает системный WASAPI endpoint.
Иное значение должно точно совпадать с уникальным именем устройства Windows.
Выбранное устройство не подменяется при ошибке.

## Сборка, проверка и запуск

Обычная проверка запускается на Windows amd64. Она выполняет `go test ./...`
и собирает `bin/assistant.exe`:

Make-цели и упаковочный `.sh`-скрипт запускайте из Git Bash или MSYS; native
integration tests ниже можно запускать из PowerShell.

```bash
make -f Makefile.windows test
```

Для отдельной проверки synthesis укажите модель; playback test запускайте с
явно выбранным устройством:

```powershell
$env:ASSISTANT_TTS_MODEL_DIR = $VoiceDir
go test -tags=tts_integration -count=1 -timeout=180s ./internal/platform/tts/sherpa
$env:ASSISTANT_PLAYBACK_TEST_DEVICE = 'Speakers (Realtek(R) Audio)'
go test -tags=playback_integration -count=1 -timeout=30s ./internal/platform/audio/output/wasapi
```

Playback test выводит короткий низкоамплитудный сигнал и требует реальное
устройство. Проверьте выбранные наушники или колонки при низкой громкости и
подтвердите слышимость вручную.

Для упаковки Windows-бинарника и DLL. Из Linux также можно выполнить эту цель,
если установлен совместимый MinGW-w64; по умолчанию используется
`x86_64-w64-mingw32-gcc`, путь можно заменить через `WINDOWS_CC`:

```bash
make -f Makefile.windows package
```

Результат появится в `dist/voice-assistant-windows-amd64`. Модель, локальный
конфиг, GGUF-файл и DLL/библиотеки LLM не входят в пакет:

```powershell
.\dist\voice-assistant-windows-amd64\run-assistant.cmd -config C:\путь\к\assistant.yaml
```

Перед использованием микрофона проверьте работу в наушниках: echo cancellation
и barge-in пока не реализованы, и микрофон может повторно захватить голос из
колонок. Скрытого отключения микрофона нет. Отмена синтеза ждёт завершения
текущей native ONNX операции; мгновенная остановка C-вызова не гарантируется.
