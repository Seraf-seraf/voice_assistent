//go:build linux && amd64 && cgo && !android && !musl

package pulse

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/Seraf-seraf/voice_assistent/internal/service/diagnostics"
	"github.com/ebitengine/purego"
)

const (
	pulseLibraryName       = "libpulse.so.0"
	pulseSampleFloat32LE   = 5
	pulseStreamStartCorked = 1
	pulseContextReady      = 4
	pulseContextFailed     = 5
	pulseContextTerminated = 6
	pulseStreamReady       = 2
	pulseStreamFailed      = 3
	pulseStreamTerminated  = 4
	pulseOperationRunning  = 0
	pulseOperationDone     = 1
	pulseOperationCanceled = 2
	pulseCleanupTimeout    = 3 * time.Second
	pulseWriteFrames       = 22050 / 50
)

type pulseAPI struct {
	library uintptr

	mainloopNew     func() uintptr
	mainloopGetAPI  func(uintptr) uintptr
	mainloopIterate func(uintptr, int32, *int32) int32
	mainloopFree    func(uintptr)

	contextNew        func(uintptr, string) uintptr
	contextConnect    func(uintptr, uintptr, int32, uintptr) int32
	contextGetState   func(uintptr) int32
	contextErrno      func(uintptr) int32
	contextDisconnect func(uintptr)
	contextUnref      func(uintptr)

	streamNew             func(uintptr, string, unsafe.Pointer, uintptr) uintptr
	streamConnectPlayback func(uintptr, uintptr, uintptr, int32, uintptr, uintptr) int32
	streamGetState        func(uintptr) int32
	streamWritableSize    func(uintptr) uintptr
	streamWrite           func(uintptr, unsafe.Pointer, uintptr, uintptr, int64, int32) int32
	streamDrain           func(uintptr, uintptr, uintptr) uintptr
	streamFlush           func(uintptr, uintptr, uintptr) uintptr
	streamCork            func(uintptr, int32, uintptr, uintptr) uintptr
	streamDisconnect      func(uintptr) int32
	streamUnref           func(uintptr)

	operationGetState func(uintptr) int32
	operationCancel   func(uintptr)
	operationUnref    func(uintptr)
	strerror          func(int32) string
}

type pulseOperationResult struct {
	called  atomic.Bool
	success atomic.Bool
}

var (
	operationCallback uintptr
	operationResults  sync.Map
	operationID       atomic.Uint64
	operationInitOnce sync.Once
)

type nativeDevice struct {
	commands  chan pulseCommand
	done      chan struct{}
	closed    atomic.Bool
	closeOnce sync.Once
	closeErr  error
}

type pulseState struct {
	api        *pulseAPI
	mainloop   uintptr
	context    uintptr
	stream     uintptr
	sampleRate int
	closed     bool
}

type pulseCommandKind uint8

const (
	pulseCommandWrite pulseCommandKind = iota
	pulseCommandDrain
	pulseCommandFlush
	pulseCommandClose
)

type pulseCommand struct {
	kind    pulseCommandKind
	ctx     context.Context
	samples []float32
	result  chan error
}

var _ backend = (*nativeDevice)(nil)

type pulseSampleSpec struct {
	format   uint32
	rate     uint32
	channels uint8
	padding  [3]byte
}

func Open(ctx context.Context, options Options) (*Player, error) {
	if err := options.Validate(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	device, err := openNativeDevice(ctx, options)
	if err != nil {
		return nil, err
	}
	diagnostics.Emit(ctx, diagnostics.Event{Phase: "output_configured", SampleRate: options.SampleRate})
	return newPlayer(device, options.SampleRate), nil
}

func openNativeDevice(ctx context.Context, options Options) (*nativeDevice, error) {
	device := &nativeDevice{commands: make(chan pulseCommand), done: make(chan struct{})}
	ready := make(chan error, 1)
	go device.run(ctx, options, ready)
	select {
	case err := <-ready:
		if err != nil {
			<-device.done
			return nil, err
		}
	case <-ctx.Done():
		_ = device.Close()
		return nil, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		_ = device.Close()
		return nil, err
	}
	return device, nil
}

func (d *nativeDevice) run(ctx context.Context, options Options, ready chan<- error) {
	// Основной цикл PulseAudio должен обслуживаться одним OS-потоком на всём сроке жизни.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(d.done)

	state := &pulseState{sampleRate: options.SampleRate}
	if err := state.open(ctx, options); err != nil {
		ready <- errors.Join(err, state.close())
		return
	}
	ready <- nil
	for {
		command := <-d.commands
		var err error
		switch command.kind {
		case pulseCommandWrite:
			err = state.write(command.ctx, command.samples)
		case pulseCommandDrain:
			err = state.drain(command.ctx)
		case pulseCommandFlush:
			err = state.flush()
		case pulseCommandClose:
			err = state.close()
			command.result <- err
			return
		default:
			err = errors.New("неизвестная команда вывода PulseAudio")
		}
		command.result <- err
	}
}

func (d *pulseState) open(ctx context.Context, options Options) error {
	api, err := loadPulseAPI()
	if err != nil {
		return fmt.Errorf("загрузить системную библиотеку PulseAudio: %w", err)
	}
	d.api = api

	d.mainloop = api.mainloopNew()
	if d.mainloop == 0 {
		return errors.New("PulseAudio не создал основной цикл событий")
	}
	mainloopAPI := api.mainloopGetAPI(d.mainloop)
	if mainloopAPI == 0 {
		return errors.New("PulseAudio не предоставил API основного цикла")
	}
	d.context = api.contextNew(mainloopAPI, "voice-assistant")
	if d.context == 0 {
		return errors.New("PulseAudio не создал клиентский контекст")
	}
	if result := api.contextConnect(d.context, 0, 0, 0); result < 0 {
		return d.apiError("подключиться к серверу PulseAudio", api.contextErrno(d.context))
	}
	if err := d.waitContextReady(ctx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	spec := pulseSampleSpec{format: pulseSampleFloat32LE, rate: uint32(options.SampleRate), channels: 1}
	d.stream = api.streamNew(d.context, "assistant speech", unsafe.Pointer(&spec), 0)
	runtime.KeepAlive(&spec)
	if d.stream == 0 {
		return errors.New("PulseAudio не создал поток воспроизведения")
	}
	deviceName := options.Device
	if deviceName == "" || deviceName == "default" {
		deviceName = ""
	}
	var devicePointer uintptr
	if deviceName != "" {
		deviceBytes := append([]byte(deviceName), 0)
		devicePointer = uintptr(unsafe.Pointer(&deviceBytes[0]))
		result := api.streamConnectPlayback(d.stream, devicePointer, 0, pulseStreamStartCorked, 0, 0)
		runtime.KeepAlive(deviceBytes)
		if result < 0 {
			return d.apiError("открыть выбранный выход PulseAudio", api.contextErrno(d.context))
		}
	} else if result := api.streamConnectPlayback(d.stream, 0, 0, pulseStreamStartCorked, 0, 0); result < 0 {
		return d.apiError("открыть выбранный выход PulseAudio", api.contextErrno(d.context))
	}
	if err := d.waitStreamReady(ctx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func loadPulseAPI() (_ *pulseAPI, resultErr error) {
	library, err := purego.Dlopen(pulseLibraryName, 2)
	if err != nil {
		return nil, fmt.Errorf("открыть %s: %w", pulseLibraryName, err)
	}
	api := &pulseAPI{library: library}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, purego.Dlclose(library))
		}
	}()
	bind := func(destination any, name string) error {
		symbol, err := purego.Dlsym(library, name)
		if err != nil {
			return fmt.Errorf("найти функцию %s: %w", name, err)
		}
		purego.RegisterFunc(destination, symbol)
		return nil
	}
	bindings := []struct {
		function any
		name     string
	}{
		{&api.mainloopNew, "pa_mainloop_new"},
		{&api.mainloopGetAPI, "pa_mainloop_get_api"},
		{&api.mainloopIterate, "pa_mainloop_iterate"},
		{&api.mainloopFree, "pa_mainloop_free"},
		{&api.contextNew, "pa_context_new"},
		{&api.contextConnect, "pa_context_connect"},
		{&api.contextGetState, "pa_context_get_state"},
		{&api.contextErrno, "pa_context_errno"},
		{&api.contextDisconnect, "pa_context_disconnect"},
		{&api.contextUnref, "pa_context_unref"},
		{&api.streamNew, "pa_stream_new"},
		{&api.streamConnectPlayback, "pa_stream_connect_playback"},
		{&api.streamGetState, "pa_stream_get_state"},
		{&api.streamWritableSize, "pa_stream_writable_size"},
		{&api.streamWrite, "pa_stream_write"},
		{&api.streamDrain, "pa_stream_drain"},
		{&api.streamFlush, "pa_stream_flush"},
		{&api.streamCork, "pa_stream_cork"},
		{&api.streamDisconnect, "pa_stream_disconnect"},
		{&api.streamUnref, "pa_stream_unref"},
		{&api.operationGetState, "pa_operation_get_state"},
		{&api.operationCancel, "pa_operation_cancel"},
		{&api.operationUnref, "pa_operation_unref"},
		{&api.strerror, "pa_strerror"},
	}
	for _, binding := range bindings {
		if err := bind(binding.function, binding.name); err != nil {
			return nil, fmt.Errorf("подготовить API PulseAudio: %w", err)
		}
	}
	operationInitOnce.Do(func() {
		operationCallback = purego.NewCallback(func(_ uintptr, success int32, userdata uintptr) {
			value, exists := operationResults.Load(uint64(userdata))
			if !exists {
				return
			}
			result := value.(*pulseOperationResult)
			result.success.Store(success != 0)
			result.called.Store(true)
		})
	})
	return api, nil
}

func (d *nativeDevice) request(command pulseCommand) error {
	if d.closed.Load() {
		return ErrClosed
	}
	command.result = make(chan error, 1)
	select {
	case d.commands <- command:
	case <-d.done:
		return ErrClosed
	}
	select {
	case err := <-command.result:
		return err
	case <-d.done:
		select {
		case err := <-command.result:
			return err
		default:
			return ErrClosed
		}
	}
}

func (d *nativeDevice) Write(ctx context.Context, samples []float32) error {
	return d.request(pulseCommand{kind: pulseCommandWrite, ctx: ctx, samples: samples})
}

func (d *nativeDevice) Drain(ctx context.Context) error {
	return d.request(pulseCommand{kind: pulseCommandDrain, ctx: ctx})
}

func (d *nativeDevice) Flush() error {
	return d.request(pulseCommand{kind: pulseCommandFlush})
}

func (d *nativeDevice) Close() error {
	d.closeOnce.Do(func() {
		d.closed.Store(true)
		result := make(chan error, 1)
		select {
		case d.commands <- pulseCommand{kind: pulseCommandClose, result: result}:
			d.closeErr = <-result
		case <-d.done:
		}
	})
	return d.closeErr
}

func (d *pulseState) write(ctx context.Context, samples []float32) error {
	if d.closed || d.stream == 0 {
		return ErrClosed
	}
	if err := d.runOperation(ctx, func(stream, callback, userdata uintptr) uintptr {
		return d.api.streamCork(stream, 0, callback, userdata)
	}, 0); err != nil {
		return fmt.Errorf("возобновить поток PulseAudio: %w", err)
	}
	started := false
	for offset := 0; offset < len(samples); {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := d.ensureStreamReady(); err != nil {
			return err
		}
		writable := d.api.streamWritableSize(d.stream)
		if writable == ^uintptr(0) {
			return d.apiError("получить свободный объём потока PulseAudio", d.api.contextErrno(d.context))
		}
		frames := int(writable / 4)
		if frames > pulseWriteFrames {
			frames = pulseWriteFrames
		}
		remaining := len(samples) - offset
		if frames > remaining {
			frames = remaining
		}
		if frames > 0 {
			start := unsafe.Pointer(&samples[offset])
			if result := d.api.streamWrite(d.stream, start, uintptr(frames*4), 0, 0, 0); result < 0 {
				runtime.KeepAlive(samples)
				return d.apiError("передать PCM в PulseAudio", d.api.contextErrno(d.context))
			}
			runtime.KeepAlive(samples)
			if !started {
				diagnostics.Emit(ctx, diagnostics.Event{Phase: "output_started", Frames: len(samples), SampleRate: d.sampleRate})
				started = true
			}
			offset += frames
			continue
		}
		if err := d.iterate(); err != nil {
			return err
		}
		if err := waitPulsePoll(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (d *pulseState) drain(ctx context.Context) error {
	if err := d.runOperation(ctx, d.api.streamDrain, 0); err != nil {
		return err
	}
	if err := d.runOperation(ctx, func(stream, callback, userdata uintptr) uintptr {
		return d.api.streamCork(stream, 1, callback, userdata)
	}, 0); err != nil {
		return fmt.Errorf("приостановить пустой поток PulseAudio: %w", err)
	}
	return nil
}

func (d *pulseState) flush() error {
	ctx, cancel := context.WithTimeout(context.Background(), pulseCleanupTimeout)
	defer cancel()
	if err := d.runOperation(ctx, d.api.streamFlush, pulseCleanupTimeout); err != nil {
		return d.disconnectAfterFlushFailure(err)
	}
	if err := d.runOperation(ctx, func(stream, callback, userdata uintptr) uintptr {
		return d.api.streamCork(stream, 1, callback, userdata)
	}, pulseCleanupTimeout); err != nil {
		return d.disconnectAfterFlushFailure(err)
	}
	return nil
}

func (d *pulseState) disconnectAfterFlushFailure(err error) error {
	if d.stream != 0 {
		_ = d.api.streamDisconnect(d.stream)
		d.api.streamUnref(d.stream)
		d.stream = 0
	}
	return fmt.Errorf("сбросить очередь PulseAudio: %w", err)
}

func (d *pulseState) runOperation(ctx context.Context, start func(uintptr, uintptr, uintptr) uintptr, timeout time.Duration) error {
	if d.closed || d.stream == 0 {
		return ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	result := &pulseOperationResult{}
	id := operationID.Add(1)
	operationResults.Store(id, result)
	operation := start(d.stream, operationCallback, uintptr(id))
	if operation == 0 {
		operationResults.Delete(id)
		return d.apiError("запустить операцию потока PulseAudio", d.api.contextErrno(d.context))
	}
	defer func() {
		operationResults.Delete(id)
		d.api.operationUnref(operation)
	}()
	var deadline time.Time
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}
	for {
		if err := ctx.Err(); err != nil {
			d.api.operationCancel(operation)
			return err
		}
		if err := d.iterate(); err != nil {
			return err
		}
		switch state := d.api.operationGetState(operation); state {
		case pulseOperationDone:
			if !result.called.Load() {
				return errors.New("PulseAudio завершил операцию без подтверждения результата")
			}
			if !result.success.Load() {
				code := d.api.contextErrno(d.context)
				if code == 0 {
					return errors.New("PulseAudio отклонил операцию потока")
				}
				return d.apiError("завершить операцию потока PulseAudio", code)
			}
			return nil
		case pulseOperationCanceled:
			return errors.New("операция потока PulseAudio отменена")
		case pulseOperationRunning:
		default:
			return fmt.Errorf("PulseAudio вернул неизвестное состояние операции %d", state)
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			d.api.operationCancel(operation)
			return context.DeadlineExceeded
		}
		if err := waitPulsePoll(ctx); err != nil {
			d.api.operationCancel(operation)
			return err
		}
	}
}

func (d *pulseState) waitContextReady(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		switch state := d.api.contextGetState(d.context); state {
		case pulseContextReady:
			return nil
		case pulseContextFailed, pulseContextTerminated:
			return d.apiError("подключиться к серверу PulseAudio", d.api.contextErrno(d.context))
		}
		if err := d.iterate(); err != nil {
			return err
		}
		if err := waitPulsePoll(ctx); err != nil {
			return err
		}
	}
}

func (d *pulseState) waitStreamReady(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		switch state := d.api.streamGetState(d.stream); state {
		case pulseStreamReady:
			return nil
		case pulseStreamFailed, pulseStreamTerminated:
			return d.apiError("подготовить поток PulseAudio", d.api.contextErrno(d.context))
		}
		if err := d.iterate(); err != nil {
			return err
		}
		if err := waitPulsePoll(ctx); err != nil {
			return err
		}
	}
}

func (d *pulseState) ensureStreamReady() error {
	switch state := d.api.streamGetState(d.stream); state {
	case pulseStreamReady:
		return nil
	case pulseStreamFailed, pulseStreamTerminated:
		return d.apiError("поток PulseAudio завершился с ошибкой", d.api.contextErrno(d.context))
	default:
		return fmt.Errorf("поток PulseAudio не готов, состояние %d", state)
	}
}

func (d *pulseState) iterate() error {
	var result int32
	if code := d.api.mainloopIterate(d.mainloop, 0, &result); code < 0 {
		return d.apiError("обработать событие PulseAudio", d.api.contextErrno(d.context))
	}
	return nil
}

func (d *pulseState) apiError(operation string, code int32) error {
	message := d.api.strerror(code)
	if message == "" {
		return fmt.Errorf("%s: код PulseAudio %d", operation, code)
	}
	return fmt.Errorf("%s: %s", operation, message)
}

func (d *pulseState) close() error {
	if d.closed {
		return nil
	}
	d.closed = true
	var resultErr error
	if d.stream != 0 {
		if code := d.api.streamDisconnect(d.stream); code < 0 {
			resultErr = errors.Join(resultErr, d.apiError("закрыть поток PulseAudio", d.api.contextErrno(d.context)))
		}
		d.api.streamUnref(d.stream)
		d.stream = 0
	}
	if d.context != 0 {
		d.api.contextDisconnect(d.context)
		d.api.contextUnref(d.context)
		d.context = 0
	}
	if d.mainloop != 0 {
		d.api.mainloopFree(d.mainloop)
		d.mainloop = 0
	}
	if d.api != nil && d.api.library != 0 {
		resultErr = errors.Join(resultErr, purego.Dlclose(d.api.library))
		d.api.library = 0
	}
	return resultErr
}

func waitPulsePoll(ctx context.Context) error {
	timer := time.NewTimer(5 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
