// Package assistant содержит application runtime для обработки событий речи.
//
// Transcriber последовательно распознаёт завершённые utterance вне event loop.
// InputProcessor нормализует raw transcription и маршрутизирует команды и
// запросы. Responder — единственная lifecycle-граница создания и завершения
// ответа через llm.Generator: во время генерации он передаёт текстовые дельты
// в ResponseSink, а полный ответ сохраняет в Dialogue только после успешного
// завершения output lifecycle. В production Responder последовательно
// вызывается STT worker. Это потоковая выдача текста, не TTS и не прерывание.
//
// ResponseSink получает для одного turn от нуля до нескольких последовательных
// Push, за которыми следует Complete либо Abort. Успешный Complete завершает
// output lifecycle; если Complete возвращает ошибку, Responder вызывает Abort
// для очистки незавершённого output.
// ResponseDelta.Text — добавочный фрагмент; Responder не передаёт пустые
// дельты, а конкатенация принятых Push.Text совпадает с Response.Text в
// Complete. Complete подтверждает доставку полного текста output-потребителю,
// но не его озвучивание. Abort выполняет ограниченную локальную очистку и не
// записывает assistant message, не вызывает Dialogue.InterruptTurn и не
// начинает длительную работу. После terminal вызова для turn новых вызовов
// нет. Sink не владеет Dialogue.Manager, Generator или context lifecycle и не
// должен писать prompt или ответ в application logger.
//
// Все методы sink вызываются последовательно одним владельцем; concurrent
// calls не поддерживаются. Отмена request context не мешает Responder вызвать
// Abort с context.WithoutCancel для локальной очистки.
package assistant
