package assistant

import "errors"

var ErrSpeechInterrupted = errors.New("ответ прерван новой речью пользователя")
