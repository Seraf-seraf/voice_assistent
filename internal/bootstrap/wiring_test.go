package bootstrap

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Seraf-seraf/voice_assistent/internal/app/assistant"
	"github.com/Seraf-seraf/voice_assistent/internal/bootstrap/config"
	"github.com/Seraf-seraf/voice_assistent/internal/platform/llm/llamacpp"
	"github.com/Seraf-seraf/voice_assistent/internal/service/audio"
	"github.com/Seraf-seraf/voice_assistent/internal/service/dialogue"
	"github.com/Seraf-seraf/voice_assistent/internal/service/llm"
	"github.com/Seraf-seraf/voice_assistent/internal/service/stt"
	"github.com/Seraf-seraf/voice_assistent/internal/service/vad"
)

func TestNewAudioFormat(t *testing.T) {
	format := newAudioFormat(config.AudioConfig{SampleRate: 16000, Channels: 1, FrameMS: 20})
	want := audio.Format{SampleRate: 16000, Channels: 1, FrameDuration: 20 * time.Millisecond}
	if format != want {
		t.Fatalf("newAudioFormat() = %+v, want %+v", format, want)
	}
}

func TestLoadLocalGeneratorLogsLifecycleWithoutExposingModelPath(t *testing.T) {
	for _, test := range []struct {
		name      string
		openErr   error
		wantFinal string
	}{
		{name: "успех", wantFinal: "Модель LLM загружена"},
		{name: "ошибка", openErr: errors.New("файл недоступен: /личный/путь/model.gguf"), wantFinal: "Ошибка загрузки модели LLM"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var logs bytes.Buffer
			log := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
			cfg := config.LLMConfig{Model: "/личный/путь/model.gguf"}
			wantGenerator := &llamacpp.Generator{}
			generator, err := loadLocalGeneratorWithStatus(
				context.Background(), cfg, log,
				func(context.Context, config.LLMConfig) (*llamacpp.Generator, error) {
					return wantGenerator, test.openErr
				},
			)
			if !errors.Is(err, test.openErr) {
				t.Fatalf("loadLocalGeneratorWithStatus() error = %v, want %v", err, test.openErr)
			}
			if test.openErr == nil && generator != wantGenerator {
				t.Fatalf("generator = %p, want %p", generator, wantGenerator)
			}
			if test.openErr != nil && generator != nil {
				t.Fatalf("generator = %p after load failure, want nil", generator)
			}
			logged := logs.String()
			startIndex := strings.Index(logged, "Начата загрузка модели LLM")
			finalIndex := strings.Index(logged, test.wantFinal)
			if startIndex < 0 || finalIndex <= startIndex {
				t.Fatalf("неверный порядок записей журнала: %s", logged)
			}
			if !strings.Contains(logged, "модель=model.gguf") || !strings.Contains(logged, "длительность=") {
				t.Fatalf("в журнале нет имени модели или длительности: %s", logged)
			}
			if strings.Contains(logged, "/личный/путь") {
				t.Fatalf("полный путь модели попал в журнал: %s", logged)
			}
		})
	}
}

func TestCompositionFactoriesOwnTheirValidation(t *testing.T) {
	transcriptCfg := config.Default().Transcript
	if _, err := newTranscriptNormalizer(transcriptCfg); err != nil {
		t.Fatal(err)
	}
	transcriptCfg.MinSignificantRunes = 0
	if _, err := newTranscriptNormalizer(transcriptCfg); err == nil {
		t.Fatal("normalizer accepted invalid minimum")
	}
	transcriptCfg = config.Default().Transcript
	transcriptCfg.IgnoredExact = []string{"  "}
	if _, err := newTranscriptNormalizer(transcriptCfg); err == nil {
		t.Fatal("normalizer accepted empty ignored exact")
	}
	transcriptCfg = config.Default().Transcript
	transcriptCfg.IgnoredPatterns = []string{"["}
	if _, err := newTranscriptNormalizer(transcriptCfg); err == nil {
		t.Fatal("normalizer accepted invalid pattern")
	}

	appCfg, wakeCfg := config.Default().App, config.Default().Wake
	if _, err := newControlRouter(config.AppConfig{Mode: "mystery"}, wakeCfg); err == nil {
		t.Fatal("router accepted unknown mode")
	}
	wakeApp := appCfg
	wakeApp.Mode = config.ModeWake
	if _, err := newControlRouter(wakeApp, config.WakeConfig{ActivationWindow: config.Duration(time.Second)}); err == nil {
		t.Fatal("wake router accepted no phrases")
	}
	if _, err := newControlRouter(wakeApp, config.WakeConfig{Phrases: []string{"ассистент"}}); err == nil {
		t.Fatal("wake router accepted zero window")
	}
	for _, mode := range []string{config.ModeAlways, config.ModePTT} {
		if _, err := newControlRouter(config.AppConfig{Mode: mode}, config.WakeConfig{}); err != nil {
			t.Errorf("%s router rejected empty wake config: %v", mode, err)
		}
	}

	if _, err := newDialogueManager(config.Default().App, config.Default().Dialogue); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name     string
		app      config.AppConfig
		dialogue config.DialogueConfig
	}{
		{name: "empty system prompt", app: config.AppConfig{ResponsePolicy: "policy"}, dialogue: config.Default().Dialogue},
		{name: "empty response policy", app: config.AppConfig{SystemPrompt: "prompt"}, dialogue: config.Default().Dialogue},
		{name: "short history", app: config.Default().App, dialogue: config.DialogueConfig{MaxHistoryMessages: 1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := newDialogueManager(test.app, test.dialogue); err == nil {
				t.Fatal("dialogue manager accepted invalid options")
			}
		})
	}
}

type wiringSTTClient struct {
	result stt.Transcript
}

type recordingGenerator struct {
	requests    []llm.Request
	generateErr error
	output      *bytes.Buffer
}

func (g *recordingGenerator) Generate(_ context.Context, request llm.Request, emit llm.Emit) error {
	g.requests = append(g.requests, request)
	if g.generateErr != nil {
		return g.generateErr
	}
	answer := "Ответ " + strconv.Itoa(len(g.requests))
	if err := emit(llm.TextDelta{Text: "Ответ"}); err != nil {
		return err
	}
	if g.output != nil {
		if !strings.HasSuffix(g.output.String(), "Ассистент: Ответ") {
			return errors.New("first response delta was not written during Generate")
		}
	}
	if err := emit(llm.TextDelta{Text: " " + strings.TrimPrefix(answer, "Ответ ")}); err != nil {
		return err
	}
	if g.output != nil && !strings.HasSuffix(g.output.String(), "Ассистент: "+answer) {
		return errors.New("second response delta was not written during Generate")
	}
	return nil
}

func (c wiringSTTClient) Transcribe(context.Context, audio.Utterance) (stt.Transcript, error) {
	return c.result, nil
}

func TestTranscriberForwardsQueryAndLogsOnlyMetadata(t *testing.T) {
	var output bytes.Buffer
	log := slog.New(slog.NewTextHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug}))
	cfg := config.Default()
	normalizer, err := newTranscriptNormalizer(cfg.Transcript)
	if err != nil {
		t.Fatal(err)
	}
	controlRouter, err := newControlRouter(cfg.App, cfg.Wake)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := newDialogueManager(cfg.App, cfg.Dialogue)
	if err != nil {
		t.Fatal(err)
	}
	processor, err := newInputProcessor(normalizer, controlRouter, manager, func(context.Context, assistant.Query) error { return nil }, log)
	if err != nil {
		t.Fatal(err)
	}
	client := wiringSTTClient{result: stt.Transcript{Text: "секретная пользовательская фраза", Duration: 125 * time.Millisecond}}
	transcriber, err := newTranscriber(client, log, processor, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if err := transcriber.Handle(context.Background(), vad.SpeechEnded{Utterance: audio.Utterance{ID: 77}}); err != nil {
		t.Fatal(err)
	}
	transcriber.CloseInput()
	if err := transcriber.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	logged := output.String()
	if strings.Contains(logged, "секретная пользовательская фраза") {
		t.Fatalf("user text leaked into log: %s", logged)
	}
	if !strings.Contains(logged, "utterance_id=77") || !strings.Contains(logged, "stt_duration=125ms") {
		t.Fatalf("safe metadata missing from log: %s", logged)
	}
	if len(manager.Snapshot().Messages) != 0 {
		t.Fatal("input processing started a dialogue turn")
	}
}

func TestTranscriberInputProcessorResponderAndOutputShareDialogueLifecycle(t *testing.T) {
	var logs, output bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	cfg := config.Default()
	normalizer, err := newTranscriptNormalizer(cfg.Transcript)
	if err != nil {
		t.Fatal(err)
	}
	controlRouter, err := newControlRouter(cfg.App, cfg.Wake)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := newDialogueManager(cfg.App, cfg.Dialogue)
	if err != nil {
		t.Fatal(err)
	}
	options, err := newGenerationOptions(cfg.LLM)
	if err != nil {
		t.Fatal(err)
	}
	responseOutput, err := newResponseOutput(&output)
	if err != nil {
		t.Fatal(err)
	}
	generator := &recordingGenerator{output: &output}
	responder, err := assistant.NewResponder(manager, generator, options, responseOutput)
	if err != nil {
		t.Fatal(err)
	}
	processor, err := newInputProcessor(normalizer, controlRouter, manager, responder.Handle, log)
	if err != nil {
		t.Fatal(err)
	}
	client := sequenceWiringSTT{results: map[uint64]stt.Transcript{
		1: {Text: "первый вопрос"}, 2: {Text: "второй вопрос"},
		3: {Text: "очисти историю"}, 4: {Text: "после очистки"},
	}}
	for _, id := range []uint64{1, 2} {
		processOneUtterance(t, client, processor, id)
	}
	if len(generator.requests) != 2 {
		t.Fatalf("generation calls=%d", len(generator.requests))
	}
	second := generator.requests[1].Dialogue.Messages
	if len(second) != 3 || second[0].Role != dialogue.RoleUser || second[1].Role != dialogue.RoleAssistant || second[2].Content != "второй вопрос" {
		t.Fatalf("second request history=%+v", second)
	}
	processOneUtterance(t, client, processor, 3)
	if len(generator.requests) != 2 {
		t.Fatal("reset command reached generator")
	}
	processOneUtterance(t, client, processor, 4)
	if len(generator.requests) != 3 || len(generator.requests[2].Dialogue.Messages) != 1 || generator.requests[2].Dialogue.Messages[0].Content != "после очистки" {
		t.Fatalf("post-reset request=%+v", generator.requests)
	}
	if got, want := output.String(), "Ассистент: Ответ 1\nАссистент: Ответ 2\nАссистент: Ответ 3\n"; got != want {
		t.Fatalf("stdout=%q want=%q", got, want)
	}
	if strings.Contains(logs.String(), "вопрос") || strings.Contains(logs.String(), "Ответ") {
		t.Fatalf("user or response text leaked to slog: %s", logs.String())
	}
}

type sequenceWiringSTT struct{ results map[uint64]stt.Transcript }

func (c sequenceWiringSTT) Transcribe(_ context.Context, utterance audio.Utterance) (stt.Transcript, error) {
	return c.results[utterance.ID], nil
}

func processOneUtterance(t *testing.T, client stt.Client, processor *assistant.InputProcessor, id uint64) {
	t.Helper()
	transcriber, err := newTranscriber(client, slog.New(slog.NewTextHandler(io.Discard, nil)), processor, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if err := transcriber.Handle(context.Background(), vad.SpeechEnded{Utterance: audio.Utterance{ID: id}}); err != nil {
		t.Fatal(err)
	}
	transcriber.CloseInput()
	if err := transcriber.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestNewGenerationOptionsUsesLLMPortValidation(t *testing.T) {
	cfg := config.Default().LLM
	if _, err := newGenerationOptions(cfg); err != nil {
		t.Fatal(err)
	}
	cfg.Temperature = math.NaN()
	if _, err := newGenerationOptions(cfg); !errors.Is(err, llm.ErrInvalidOptions) {
		t.Fatalf("NaN validation error=%v", err)
	}
}

func TestNewVADComponents(t *testing.T) {
	format := newAudioFormat(config.Default().Audio)
	vadCfg := config.Default().VAD
	if _, err := newVADComponents(format, vadCfg); err != nil {
		t.Fatalf("newVADComponents() error: %v", err)
	}
}

func TestNewSTTClient(t *testing.T) {
	if _, err := newSTTClient(config.Default().STT); err != nil {
		t.Fatalf("newSTTClient() error: %v", err)
	}
}

func TestNewSTTClientRejectsInvalidOptions(t *testing.T) {
	tests := []struct {
		name   string
		modify func(*config.STTConfig)
	}{
		{
			name: "URL without http or https",
			modify: func(cfg *config.STTConfig) {
				cfg.URL = "ftp://example.com/transcribe"
			},
		},
		{
			name: "URL without host",
			modify: func(cfg *config.STTConfig) {
				cfg.URL = "http:///transcribe"
			},
		},
		{
			name: "URL with credentials",
			modify: func(cfg *config.STTConfig) {
				cfg.URL = "http://user:secret@example.com/transcribe"
			},
		},
		{
			name: "non-positive timeout",
			modify: func(cfg *config.STTConfig) {
				cfg.Timeout = 0
			},
		},
		{
			name: "non-positive response limit",
			modify: func(cfg *config.STTConfig) {
				cfg.MaxResponseBytes = 0
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := config.Default().STT
			test.modify(&cfg)
			if _, err := newSTTClient(cfg); err == nil {
				t.Fatal("newSTTClient() succeeded, want HTTP client error")
			}
		})
	}
}

func TestNewVADComponentsRejectsWebRTCDetectorSettings(t *testing.T) {
	tests := []struct {
		name   string
		modify func(*config.AudioConfig, *config.VADConfig)
	}{
		{
			name: "sample rate",
			modify: func(audioCfg *config.AudioConfig, _ *config.VADConfig) {
				audioCfg.SampleRate = 8000
			},
		},
		{
			name: "channels",
			modify: func(audioCfg *config.AudioConfig, _ *config.VADConfig) {
				audioCfg.Channels = 2
			},
		},
		{
			name: "frame duration",
			modify: func(audioCfg *config.AudioConfig, _ *config.VADConfig) {
				audioCfg.FrameMS = 15
			},
		},
		{
			name: "aggressiveness",
			modify: func(_ *config.AudioConfig, vadCfg *config.VADConfig) {
				vadCfg.Aggressiveness = 4
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			audioCfg := config.Default().Audio
			vadCfg := config.Default().VAD
			test.modify(&audioCfg, &vadCfg)
			format := newAudioFormat(audioCfg)
			if _, err := newVADComponents(format, vadCfg); err == nil {
				t.Fatal("newVADComponents() succeeded, want detector error")
			}
		})
	}
}

func TestNewVADComponentsRejectsSegmenterSettings(t *testing.T) {
	tests := []struct {
		name   string
		modify func(*config.VADConfig)
	}{
		{
			name: "invalid intervals",
			modify: func(vadCfg *config.VADConfig) {
				vadCfg.PreRoll = config.Duration(-time.Millisecond)
			},
		},
		{
			name: "max utterance not greater than min speech",
			modify: func(vadCfg *config.VADConfig) {
				vadCfg.MaxUtterance = vadCfg.MinSpeech
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			format := newAudioFormat(config.Default().Audio)
			vadCfg := config.Default().VAD
			test.modify(&vadCfg)
			if _, err := newVADComponents(format, vadCfg); err == nil {
				t.Fatal("newVADComponents() succeeded, want segmenter error")
			}
		})
	}
}
