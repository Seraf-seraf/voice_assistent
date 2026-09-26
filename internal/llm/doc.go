// Package llm defines a transport-neutral streaming text generation port.
//
// Generator.Generate blocks until generation is complete. Its returned error,
// or nil, is the only final result. The emit callback is required and is called
// sequentially in text order, never concurrently. Generate must not emit or
// continue work after it returns. Each TextDelta.Text is an additive fragment,
// not cumulative text and not necessarily one token. Fragments contain valid
// UTF-8 without model markers or internal reasoning; decoding incomplete UTF-8
// bytes belongs to the adapter. Empty fragments add nothing. Whitespace and
// word boundaries are preserved exactly. When emit returns an error, generation
// stops and Generate returns an error preserving that cause; ignoring the error
// and returning nil violates the contract. The supplied context flows through
// generation; observed cancellation ends the call with an error matching
// context.Canceled or context.DeadlineExceeded as appropriate.
//
// Request and its Dialogue snapshot are borrowed for the duration of Generate:
// the generator must not mutate them or retain them afterward. Concurrent calls
// on one Generator are outside this contract; one owner calls it sequentially.
// Options are validated before any text is emitted. MaxTokens limits new model
// tokens, not characters or total prompt size.
package llm
