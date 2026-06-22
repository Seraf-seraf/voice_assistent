// Package input определяет контракт источника звука и преобразует непрерывный
// PCM16-поток аудиоустройства в канонические frames фиксированной длины.
//
// Framer не блокирует callback аудиоустройства: при заполнении bounded queue
// новый frame отбрасывается и учитывается в метрике DroppedFrames. Malgo source
// использует miniaudio для доступа к WASAPI и другим платформенным backends.
package input
