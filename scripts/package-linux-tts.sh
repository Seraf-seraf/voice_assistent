#!/usr/bin/env bash
set -euo pipefail

expected_version="v1.12.35"
module_path="github.com/k2-fsa/sherpa-onnx-go-linux"
package_name="voice-assistant-linux-amd64"
script_dir="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(CDPATH= cd -- "$script_dir/.." && pwd)"
dist_root="$repo_root/dist"
package_dir="$dist_root/$package_name"
assistant_binary="$repo_root/bin/assistant"

cd "$repo_root"

if [[ "$(go env GOOS)" != "linux" || "$(go env GOARCH)" != "amd64" || "$(go env CGO_ENABLED)" != "1" ]]; then
	printf 'package-linux-tts требует Linux/amd64 и CGO_ENABLED=1\n' >&2
	exit 1
fi
for tool in readelf ldd; do
	if ! command -v "$tool" >/dev/null 2>&1; then
		printf 'Не найдена обязательная команда: %s\n' "$tool" >&2
		exit 1
	fi
done

actual_version="$(go list -m -f '{{.Version}}' "$module_path")"
if [[ "$actual_version" != "$expected_version" ]]; then
	printf 'Версия %s: %s, ожидалась %s\n' "$module_path" "$actual_version" "$expected_version" >&2
	exit 1
fi
if [[ ! -x "$assistant_binary" ]]; then
	printf 'Не найден собранный Linux-бинарник: %s\n' "$assistant_binary" >&2
	exit 1
fi

module_dir="$(go list -m -f '{{.Dir}}' "$module_path")"
source_lib_dir="$module_dir/lib/x86_64-unknown-linux-gnu"
if [[ ! -d "$source_lib_dir" || ! -f "$module_dir/LICENSE" ]]; then
	printf 'В модуле отсутствуют native libraries или LICENSE: %s\n' "$module_dir" >&2
	exit 1
fi

mkdir -p "$dist_root"
staging_dir="$(mktemp -d "$dist_root/.${package_name}.tmp.XXXXXX")"
cleanup() {
	case "$staging_dir" in
		"$dist_root"/."$package_name".tmp.*) rm -rf -- "$staging_dir" ;;
		*) printf 'Не удаляю staging вне ожидаемого каталога: %s\n' "$staging_dir" >&2 ;;
	esac
}
trap cleanup EXIT

mkdir -p "$staging_dir/lib" "$staging_dir/licenses"
cp "$assistant_binary" "$staging_dir/assistant"
cp "$module_dir/LICENSE" "$staging_dir/licenses/sherpa-onnx-go-linux-LICENSE"

copied=0
shopt -s nullglob
for source_library in "$source_lib_dir"/lib*.so*; do
	if [[ ! -e "$source_library" && ! -L "$source_library" ]]; then
		continue
	fi
	library_name="$(basename "$source_library")"
	if [[ -e "$staging_dir/lib/$library_name" ]]; then
		printf 'Повтор имени native library: %s\n' "$library_name" >&2
		exit 1
	fi
	cp -L "$source_library" "$staging_dir/lib/$library_name"
	soname="$(readelf -d "$staging_dir/lib/$library_name" | awk -F'[][]' '/\(SONAME\)/ { print $2; exit }')"
	if [[ -z "$soname" || "$soname" == */* || "$soname" == "." || "$soname" == ".." ]]; then
		printf 'Некорректный или отсутствующий SONAME у %s: %q\n' "$library_name" "$soname" >&2
		exit 1
	fi
	if [[ "$library_name" != "$soname" ]]; then
		soname_path="$staging_dir/lib/$soname"
		if [[ -e "$soname_path" || -L "$soname_path" ]]; then
			if ! cmp -s "$staging_dir/lib/$library_name" "$soname_path"; then
				printf 'SONAME %s уже занят другой библиотекой\n' "$soname" >&2
				exit 1
			fi
		else
			ln -s "$library_name" "$soname_path"
		fi
	fi
	copied=$((copied + 1))
done
shopt -u nullglob
if [[ "$copied" -eq 0 ]]; then
	printf 'В закреплённом модуле не найдены lib*.so* в %s\n' "$source_lib_dir" >&2
	exit 1
fi

cat >"$staging_dir/run-assistant" <<'RUNNER'
#!/usr/bin/env bash
set -euo pipefail
package_dir="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
export LD_LIBRARY_PATH="$package_dir/lib${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
exec "$package_dir/assistant" "$@"
RUNNER
chmod 0755 "$staging_dir/run-assistant"

{
	cat <<'NOTICE'
В пакет встроены shared libraries из github.com/k2-fsa/sherpa-onnx-go-linux v1.12.35.
Скопирован LICENSE Go-модуля, но он не заменяет лицензии sherpa-onnx, ONNX Runtime,
других third-party компонентов или голосовой модели. Перед распространением
проверьте notices и условия каждой зависимости отдельно.

Файлы native libraries:
NOTICE
	for library in "$staging_dir"/lib/lib*.so*; do
		printf '  %s\n' "$(basename "$library")"
	done
} >"$staging_dir/native-dependencies.txt"

ldd_output="$(LD_LIBRARY_PATH="$staging_dir/lib${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}" ldd "$staging_dir/assistant")"
if grep -q 'not found' <<<"$ldd_output"; then
	printf 'У package binary отсутствуют runtime libraries:\n%s\n' "$ldd_output" >&2
	exit 1
fi
for required_library in libsherpa-onnx-c-api libonnxruntime; do
	resolved_path="$(awk -v name="$required_library" '$1 ~ name { if ($2 == "=>") print $3; else print $1; exit }' <<<"$ldd_output")"
	case "$resolved_path" in
		"$staging_dir"/lib/*) ;;
		*)
			printf '%s разрешилась вне staging/lib: %s\n%s\n' "$required_library" "$resolved_path" "$ldd_output" >&2
			exit 1
			;;
	esac
done

mkdir -p "$package_dir"
cp -a --remove-destination "$staging_dir"/. "$package_dir"/
printf 'Пакет создан: %s\n' "$package_dir"
