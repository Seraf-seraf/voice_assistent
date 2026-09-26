// Package assistant содержит application runtime для обработки событий речи.
//
// Transcriber принимает завершённые utterance из runtime и последовательно
// передаёт их STT-клиенту вне event loop.
package assistant
