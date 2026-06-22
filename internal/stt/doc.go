// Package stt распознаёт пользовательские аудиореплики через внешний
// whisper.cpp server.
//
// HTTP client кодирует канонический PCM16 utterance в WAV в памяти, отправляет
// multipart-запрос на /inference и ограничивает время и размер ответа сервера.
package stt
