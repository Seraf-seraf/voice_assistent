#!/usr/bin/env bash
set -euo pipefail

expected_version="v1.12.35"
module_path="github.com/k2-fsa/sherpa-onnx-go-windows"
package_name="voice-assistant-windows-amd64"
script_dir="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(CDPATH= cd -- "$script_dir/.." && pwd)"
dist_root="$repo_root/dist"
package_dir="$dist_root/$package_name"
assistant_binary="$repo_root/bin/assistant.exe"

cd "$repo_root"

actual_version="$(go list -m -f '{{.Version}}' "$module_path")"
if [[ "$actual_version" != "$expected_version" ]]; then
	printf 'Версия %s: %s, ожидалась %s\n' "$module_path" "$actual_version" "$expected_version" >&2
	exit 1
fi
if [[ ! -f "$assistant_binary" ]]; then
	printf 'Не найден собранный Windows-бинарник: %s\n' "$assistant_binary" >&2
	exit 1
fi

module_dir="$(go list -m -f '{{.Dir}}' "$module_path")"
source_dll_dir="$module_dir/lib/x86_64-pc-windows-gnu"
if [[ ! -d "$source_dll_dir" || ! -f "$module_dir/LICENSE" ]]; then
	printf 'В модуле отсутствуют native DLL или LICENSE: %s\n' "$module_dir" >&2
	exit 1
fi

mkdir -p "$dist_root"
staging_dir="$(mktemp -d "$dist_root/.$package_name.tmp.XXXXXX")"
cleanup() {
	case "$staging_dir" in
		"$dist_root"/".$package_name".tmp.*) rm -rf -- "$staging_dir" ;;
		*) printf 'Не удаляю staging вне ожидаемого каталога: %s\n' "$staging_dir" >&2 ;;
	esac
}
trap cleanup EXIT

mkdir -p "$staging_dir/licenses"
cp "$assistant_binary" "$staging_dir/assistant.exe"
cp "$module_dir/LICENSE" "$staging_dir/licenses/sherpa-onnx-go-windows-LICENSE"

copied=0
shopt -s nullglob
for source_library in "$source_dll_dir"/*.dll; do
	cp "$source_library" "$staging_dir/$(basename "$source_library")"
	copied=$((copied + 1))
done
shopt -u nullglob
if [[ "$copied" -eq 0 ]]; then
	printf 'В закреплённом модуле не найдены DLL в %s\n' "$source_dll_dir" >&2
	exit 1
fi
for required_library in onnxruntime.dll sherpa-onnx-c-api.dll sherpa-onnx-cxx-api.dll; do
	if [[ ! -f "$staging_dir/$required_library" ]]; then
		printf 'В пакете отсутствует требуемая библиотека: %s\n' "$required_library" >&2
		exit 1
	fi
done

cat >"$staging_dir/run-assistant.cmd" <<'RUNNER'
@echo off
set "PACKAGE_DIR=%~dp0"
set "PATH=%PACKAGE_DIR%;%PATH%"
"%PACKAGE_DIR%assistant.exe" %*
RUNNER

{
	cat <<'NOTICE'
В пакет встроены DLL из github.com/k2-fsa/sherpa-onnx-go-windows v1.12.35.
Скопирован LICENSE Go-модуля, но он не заменяет лицензии sherpa-onnx, ONNX Runtime,
других third-party компонентов или голосовой модели. Перед распространением
проверьте notices и условия каждой зависимости отдельно.

Файлы native libraries:
NOTICE
	for library in "$staging_dir"/*.dll; do
		printf '  %s\n' "$(basename "$library")"
	done
} >"$staging_dir/native-dependencies.txt"

mkdir -p "$package_dir"
cp -a --remove-destination "$staging_dir"/. "$package_dir"/
printf 'Пакет создан: %s\n' "$package_dir"
