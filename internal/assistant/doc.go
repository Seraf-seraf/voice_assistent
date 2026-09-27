// Package assistant содержит application runtime для обработки событий речи.
//
// Transcriber последовательно распознаёт завершённые utterance вне event loop.
// InputProcessor нормализует raw transcription и маршрутизирует команды и
// запросы. Responder — единственная lifecycle-граница создания и завершения
// ответа через llm.Generator: он начинает turn, передаёт snapshot генератору,
// доставляет целый ответ и завершает либо освобождает turn. В production он
// последовательно вызывается STT worker и выдаёт завершённый текстовый ответ.
package assistant
