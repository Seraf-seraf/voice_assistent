// Package listener связывает источник микрофона, detector голосовой активности
// и segmenter пользовательских реплик.
//
// Listener последовательно обрабатывает frames в одной goroutine, поэтому
// stateful VAD не вызывается конкурентно. События начала и конца речи не
// отбрасываются: при заполнении event queue обработка создаёт backpressure.
package listener
