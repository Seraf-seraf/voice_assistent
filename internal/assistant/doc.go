// Package assistant содержит application runtime для обработки событий речи.
//
// SpeechEventHandler вызывается последовательно и должен быть коротким и
// уважать context. Длительную работу нельзя выполнять непосредственно внутри
// event loop.
package assistant
