// Package assistant содержит application runtime для обработки событий речи.
//
// Transcriber последовательно распознаёт завершённые utterance вне event loop.
// InputProcessor нормализует raw transcription и маршрутизирует команды и
// запросы; начало dialogue turn остаётся ответственностью будущего LLM consumer.
package assistant
