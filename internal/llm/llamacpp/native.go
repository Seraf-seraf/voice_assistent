package llamacpp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/Seraf-seraf/voice_assistent/internal/llm"
	"github.com/hybridgroup/yzma/pkg/llama"
)

const prefillBatchTokens = 128

var nativeLifecycle struct {
	sync.Mutex
	active bool
}

type requestAbort struct{ ctx context.Context }

type nativeBackend struct {
	model       llama.Model
	context     llama.Context
	vocab       llama.Vocab
	template    string
	contextSize uint32
	slot        atomic.Pointer[requestAbort]
	sampler     llama.Sampler
	pending     llama.Token
	position    int32
	closed      bool
}

func openNative(ctx context.Context, options Options) (_ *Generator, resultErr error) {
	modelPath, err := filepath.Abs(options.ModelPath)
	if err != nil {
		return nil, fmt.Errorf("resolve model path: %w", err)
	}
	libraryDir, err := filepath.Abs(options.LibraryDir)
	if err != nil {
		return nil, fmt.Errorf("resolve native library path: %w", err)
	}
	modelFile, err := os.Open(modelPath)
	if err != nil {
		return nil, fmt.Errorf("open GGUF model: %w", err)
	}
	info, err := modelFile.Stat()
	if err != nil {
		_ = modelFile.Close()
		return nil, fmt.Errorf("stat GGUF model: %w", err)
	}
	if !info.Mode().IsRegular() {
		_ = modelFile.Close()
		return nil, fmt.Errorf("model path is not a regular file")
	}
	var magic [4]byte
	_, readErr := io.ReadFull(modelFile, magic[:])
	closeErr := modelFile.Close()
	if readErr != nil {
		return nil, fmt.Errorf("read GGUF header: %w", readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close GGUF model: %w", closeErr)
	}
	if string(magic[:]) != "GGUF" {
		return nil, fmt.Errorf("model file has invalid GGUF magic")
	}
	libInfo, err := os.Stat(libraryDir)
	if err != nil {
		return nil, fmt.Errorf("stat native library directory: %w", err)
	}
	if !libInfo.IsDir() {
		return nil, fmt.Errorf("native library path is not a directory")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	nativeLifecycle.Lock()
	if nativeLifecycle.active {
		nativeLifecycle.Unlock()
		return nil, ErrBusy
	}
	nativeLifecycle.active = true
	nativeLifecycle.Unlock()
	backend := &nativeBackend{pending: llama.TokenNull}
	loaded := false
	backendInitialized := false
	defer func() {
		if resultErr == nil {
			return
		}
		if loaded || backendInitialized {
			resultErr = errors.Join(resultErr, backend.Close())
		} else {
			nativeLifecycle.Lock()
			nativeLifecycle.active = false
			nativeLifecycle.Unlock()
		}
	}()

	if err := llama.Load(libraryDir); err != nil {
		return nil, fmt.Errorf("load llama.cpp libraries: %w", err)
	}
	loaded = true
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	llama.LogSet(llama.LogSilent())
	llama.BackendInit()
	backendInitialized = true
	if err := llama.GGMLBackendLoadAllFromPath(libraryDir); err != nil {
		return nil, fmt.Errorf("load llama.cpp backends: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if options.GPULayers > 0 {
		if !llama.SupportsGpuOffload() || !hasGPUDevice() {
			return nil, ErrGPUUnavailable
		}
	}
	modelParams := llama.ModelDefaultParams()
	modelParams.NGpuLayers = int32(options.GPULayers)
	backend.model, err = llama.ModelLoadFromFile(modelPath, modelParams)
	if err != nil {
		return nil, fmt.Errorf("load GGUF model: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	contextParams := llama.ContextDefaultParams()
	contextParams.NCtx = uint32(options.ContextSize)
	contextParams.NBatch = prefillBatchTokens
	contextParams.NUbatch = prefillBatchTokens
	contextParams.NSeqMax = 1
	contextParams.NThreadsBatch = int32(options.Threads)
	contextParams.NThreads = int32(options.Threads)
	backend.context, err = llama.InitFromModel(backend.model, contextParams)
	if err != nil {
		return nil, fmt.Errorf("initialize llama.cpp context: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	backend.contextSize = llama.NCtx(backend.context)
	if backend.contextSize == 0 {
		return nil, fmt.Errorf("native context has zero tokens")
	}
	backend.vocab = llama.ModelGetVocab(backend.model)
	if backend.vocab == 0 {
		return nil, fmt.Errorf("model vocabulary is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	backend.template = llama.ModelChatTemplate(backend.model, "")
	if backend.template == "" {
		return nil, fmt.Errorf("model chat template is unavailable")
	}
	slot := &backend.slot
	llama.SetAbortCallback(backend.context, func() bool {
		request := slot.Load()
		return request != nil && request.ctx.Err() != nil
	})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	generator, err := newGenerator(backend, options.Timeout)
	if err != nil {
		return nil, err
	}
	return generator, nil
}

func hasGPUDevice() bool {
	for i, count := uint64(0), llama.GGMLBackendDeviceCount(); i < count; i++ {
		device := llama.GGMLBackendDeviceGet(i)
		if device != 0 && llama.GGMLBackendDevType(device) == llama.GGMLBackendDeviceTypeGPU {
			return true
		}
	}
	return false
}

func (b *nativeBackend) Template() string { return b.template }

func (b *nativeBackend) Begin(ctx context.Context, prompt string, options llm.Options) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := options.Validate(); err != nil {
		return err
	}
	if int64(len(prompt)) > math.MaxInt32 {
		return fmt.Errorf("%w: prompt exceeds native byte limit", ErrInvalidRequest)
	}
	tokens := llama.Tokenize(b.vocab, prompt, true, true)
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(tokens) == 0 || int64(len(tokens)) > math.MaxInt32 {
		return fmt.Errorf("%w: пустой или слишком большой prompt", ErrInvalidRequest)
	}
	if uint64(len(tokens)) > uint64(b.contextSize) || uint64(options.MaxTokens) > uint64(b.contextSize)-uint64(len(tokens)) {
		return ErrContextLimit
	}
	memory, err := llama.GetMemory(b.context)
	if err != nil {
		return fmt.Errorf("get model memory: %w", err)
	}
	if memory == 0 {
		return fmt.Errorf("model memory is unavailable")
	}
	if err := llama.MemoryClear(memory, true); err != nil {
		return fmt.Errorf("clear model memory: %w", err)
	}
	b.sampler = makeSampler(options)
	if b.sampler == 0 {
		return fmt.Errorf("initialize sampler: null handle")
	}
	b.pending = llama.TokenNull
	b.position = int32(len(tokens))
	b.slot.Store(&requestAbort{ctx: ctx})
	batch := llama.BatchInit(prefillBatchTokens, 0, 1)
	if batch.Token == nil {
		_ = llama.BatchFree(batch)
		return fmt.Errorf("allocate prefill batch: null handle")
	}
	defer llama.BatchFree(batch)
	for offset := 0; offset < len(tokens); offset += prefillBatchTokens {
		if err := ctx.Err(); err != nil {
			return err
		}
		batch.Clear()
		end := offset + prefillBatchTokens
		if end > len(tokens) {
			end = len(tokens)
		}
		for i, token := range tokens[offset:end] {
			pos := offset + i
			if err := batch.Add(token, llama.Pos(pos), []llama.SeqId{0}, pos == len(tokens)-1); err != nil {
				return fmt.Errorf("fill prefill batch: %w", err)
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		status, err := llama.Decode(b.context, batch)
		if err != nil {
			return fmt.Errorf("decode prefill: %w", err)
		}
		if status != 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
			return fmt.Errorf("decode prefill returned code %d", status)
		}
	}
	return ctx.Err()
}

func makeSampler(options llm.Options) llama.Sampler {
	chain := llama.SamplerChainInit(llama.SamplerChainDefaultParams())
	if chain == 0 {
		return 0
	}
	if options.Temperature == 0 {
		greedy := llama.SamplerInitGreedy()
		if greedy == 0 {
			llama.SamplerFree(chain)
			return 0
		}
		llama.SamplerChainAdd(chain, greedy)
		return chain
	}
	for _, create := range []func() llama.Sampler{
		func() llama.Sampler { return llama.SamplerInitTopK(20) },
		func() llama.Sampler { return llama.SamplerInitTemp(float32(options.Temperature)) },
		func() llama.Sampler { return llama.SamplerInitDist(42) },
	} {
		sampler := create()
		if sampler == 0 {
			llama.SamplerFree(chain)
			return 0
		}
		llama.SamplerChainAdd(chain, sampler)
	}
	return chain
}

func (b *nativeBackend) Next(ctx context.Context) (tokenPiece, error) {
	if err := ctx.Err(); err != nil {
		return tokenPiece{}, err
	}
	if b.pending != llama.TokenNull {
		batch := llama.BatchGetOne([]llama.Token{b.pending})
		status, err := llama.Decode(b.context, batch)
		if err != nil {
			return tokenPiece{}, fmt.Errorf("decode generated token: %w", err)
		}
		if status != 0 {
			if err := ctx.Err(); err != nil {
				return tokenPiece{}, err
			}
			return tokenPiece{}, fmt.Errorf("decode generated token returned code %d", status)
		}
		b.position++
		b.pending = llama.TokenNull
	}
	if err := ctx.Err(); err != nil {
		return tokenPiece{}, err
	}
	token := llama.SamplerSample(b.sampler, b.context, -1)
	if token == llama.TokenNull {
		return tokenPiece{}, fmt.Errorf("sampler returned null token")
	}
	if token < 0 || token >= llama.Token(llama.VocabNTokens(b.vocab)) {
		return tokenPiece{}, fmt.Errorf("%w: sampled token is outside the vocabulary", ErrInvalidOutput)
	}
	if llama.VocabIsEOG(b.vocab, token) {
		return tokenPiece{End: true}, nil
	}
	llama.SamplerAccept(b.sampler, token)
	piece, err := tokenToBytes(b.vocab, token)
	if err != nil {
		return tokenPiece{}, err
	}
	b.pending = token
	return tokenPiece{Bytes: piece}, nil
}

func tokenToBytes(vocab llama.Vocab, token llama.Token) ([]byte, error) {
	buffer := make([]byte, 64)
	for {
		length := llama.TokenToPiece(vocab, token, buffer, 0, true)
		if length >= 0 {
			if int64(length) > int64(len(buffer)) {
				return nil, fmt.Errorf("%w: token piece length out of range", ErrInvalidOutput)
			}
			return append([]byte(nil), buffer[:length]...), nil
		}
		needed := -int64(length)
		if needed <= int64(len(buffer)) || needed > math.MaxInt32 {
			return nil, fmt.Errorf("%w: invalid token piece size", ErrInvalidOutput)
		}
		buffer = make([]byte, int(needed))
	}
}

func (b *nativeBackend) End() error {
	var result error
	if b.context != 0 {
		if err := llama.Synchronize(b.context); err != nil {
			result = errors.Join(result, fmt.Errorf("synchronize context: %w", err))
		}
	}
	b.slot.Store(nil)
	if b.sampler != 0 {
		llama.SamplerFree(b.sampler)
		b.sampler = 0
	}
	if b.context != 0 {
		memory, err := llama.GetMemory(b.context)
		if err != nil {
			result = errors.Join(result, fmt.Errorf("get memory for clear: %w", err))
		} else if memory != 0 {
			if err := llama.MemoryClear(memory, true); err != nil {
				result = errors.Join(result, fmt.Errorf("clear request memory: %w", err))
			}
		}
	}
	b.pending = llama.TokenNull
	b.position = 0
	return result
}

func (b *nativeBackend) Close() error {
	if b.closed {
		return nil
	}
	b.closed = true
	var result error
	if b.context != 0 {
		if err := llama.Synchronize(b.context); err != nil {
			result = errors.Join(result, fmt.Errorf("synchronize context: %w", err))
		}
		if err := llama.Free(b.context); err != nil {
			result = errors.Join(result, fmt.Errorf("free context: %w", err))
		}
		b.context = 0
	}
	if b.model != 0 {
		if err := llama.ModelFree(b.model); err != nil {
			result = errors.Join(result, fmt.Errorf("free model: %w", err))
		}
		b.model = 0
	}
	llama.Close()
	nativeLifecycle.Lock()
	nativeLifecycle.active = false
	nativeLifecycle.Unlock()
	return result
}
