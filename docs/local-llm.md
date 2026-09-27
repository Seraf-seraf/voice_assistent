# Локальная LLM

Production adapter загружает модель один раз при запуске процесса и использует
нейтральный `llm.Generator`. Для LLM не запускаются HTTP-сервис или subprocess.
Микрофонный тракт вызывает STT/Whisper через существующий HTTP adapter.

Закреплённые версии: Go toolchain `go1.27.1`, YZMA `v1.28.0`, native
llama.cpp `v0.5.0`. Рабочий профиль: amd64, CUDA 12, context 4096, 99 GPU
layers, 4 threads, timeout генерации 30s. Автоматического CPU fallback нет.

## Файл модели

Используется существующий файл пользователя:

```text
Qwen3.5-0.8B-Q4_K_M.gguf
```

Linux/WSL путь:

```text
/home/seraf/.lmstudio/models/lmstudio-community/Qwen3.5-0.8B-GGUF/Qwen3.5-0.8B-Q4_K_M.gguf
```

Windows путь для Windows процесса:

```text
\\wsl.localhost\Ubuntu\home\seraf\.lmstudio\models\lmstudio-community\Qwen3.5-0.8B-GGUF\Qwen3.5-0.8B-Q4_K_M.gguf
```

Путь Linux/WSL и Windows указывает на тот же файл. Приложение не копирует,
перезаписывает и не скачивает веса. Справочный опубликованный SHA-256:
`f5b14da98939b60bbe1019a964eba656407e1e0b64f1fe3003ff6d650e93bfec`.
Путь задаётся в `ASSISTANT_LLM_MODEL` или поле `llm.model` локального YAML.

## Ubuntu / WSL

Из корня репозитория:

```bash
export GOTOOLCHAIN=go1.27.1
export ASSISTANT_LLM_MODEL='/home/seraf/.lmstudio/models/lmstudio-community/Qwen3.5-0.8B-GGUF/Qwen3.5-0.8B-Q4_K_M.gguf'
export ASSISTANT_LLM_LIBRARY_DIR="$PWD/.native/llama-v0.5.0/linux-amd64-cuda12"

dpkg-query -W -f='${Status}\n' libffi8
nvidia-smi
make install-llamacpp-cuda12
make test-llamacpp-integration
```

`libffi8` требуется Go FFI binding при загрузке пакета; Makefile не устанавливает
системные пакеты. `nvidia-smi` проверяет драйвер, но inference на GPU отдельно
подтверждает только native test. Native libraries (SO и их runtime dependencies)
должны оставаться доступны рядом с приложением.

Для обычного запуска сначала заполните `llm.model` и `llm.library_dir` в
`config/assistant.yaml`, затем используйте существующую команду `make run`.
Сервис Whisper запускается отдельно, как и раньше.

## Windows PowerShell

Используйте Windows native bundle и Windows Go процесс:

```powershell
$env:GOTOOLCHAIN = 'go1.27.1'
$env:ASSISTANT_LLM_MODEL = '\\wsl.localhost\Ubuntu\home\seraf\.lmstudio\models\lmstudio-community\Qwen3.5-0.8B-GGUF\Qwen3.5-0.8B-Q4_K_M.gguf'
$env:ASSISTANT_LLM_LIBRARY_DIR = Join-Path $PWD '.native\llama-v0.5.0\windows-amd64-cuda12'

go run github.com/hybridgroup/yzma@v1.28.0 install --version v0.5.0 --processor cuda-12 --os windows --lib $env:ASSISTANT_LLM_LIBRARY_DIR --verify require
if ($LASTEXITCODE -ne 0) { throw 'Native install failed' }
go run github.com/hybridgroup/yzma@v1.28.0 verify --version v0.5.0 --lib $env:ASSISTANT_LLM_LIBRARY_DIR --strict
if ($LASTEXITCODE -ne 0) { throw 'Native verification failed' }
go test -tags=llm_integration -count=1 -timeout=180s ./internal/llm/llamacpp
if ($LASTEXITCODE -ne 0) { throw 'Native tests failed' }
```

Для голосовой сборки Windows также требуется CGO C compiler и доступное
устройство WASAPI. Windows DLL нельзя подменять Linux SO из WSL.

## Ограничения

Native Generator выдаёт текстовые `TextDelta` по мере генерации. `Responder`
сразу передаёт их в output sink, а после успешного завершения вывода сохраняет
собранный полный ответ в истории. `TextDelta` — фрагмент текста, не обязательно
один токен модели; частота отображения зависит от размера фрагментов. Это
текстовый streaming, не речевое воспроизведение. TTS не подключён. PTT hotkey
не подключён, а голосовая команда «стоп» не прерывает текущую генерацию.
