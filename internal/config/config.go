package config

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	ModeAlways = "always"
	ModePTT    = "ptt"
	ModeWake   = "wake"
)

// Duration поддерживает привычную запись интервалов: 600ms, 10s, 2m.
type Duration time.Duration

func (d *Duration) UnmarshalText(text []byte) error {
	value, err := time.ParseDuration(string(text))
	if err != nil {
		return fmt.Errorf("разобрать длительность %q: %w", text, err)
	}
	*d = Duration(value)
	return nil
}

func (d Duration) MarshalText() ([]byte, error) {
	return []byte(time.Duration(d).String()), nil
}

func (d Duration) Std() time.Duration {
	return time.Duration(d)
}

type Config struct {
	App        AppConfig        `yaml:"app"`
	Log        LogConfig        `yaml:"log"`
	Audio      AudioConfig      `yaml:"audio"`
	VAD        VADConfig        `yaml:"vad"`
	STT        STTConfig        `yaml:"stt"`
	LLM        LLMConfig        `yaml:"llm"`
	TTS        TTSConfig        `yaml:"tts"`
	Dialogue   DialogueConfig   `yaml:"dialogue"`
	Transcript TranscriptConfig `yaml:"transcript"`
	Wake       WakeConfig       `yaml:"wake"`
	Storage    StorageConfig    `yaml:"storage"`
	Control    ControlConfig    `yaml:"control"`
}

type AppConfig struct {
	Mode           string `yaml:"mode"`
	SystemPrompt   string `yaml:"system_prompt"`
	ResponsePolicy string `yaml:"response_policy"`
}

type LogConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

type AudioConfig struct {
	InputDevice  string `yaml:"input_device"`
	OutputDevice string `yaml:"output_device"`
	SampleRate   int    `yaml:"sample_rate"`
	Channels     int    `yaml:"channels"`
	FrameMS      int    `yaml:"frame_ms"`
	BufferFrames int    `yaml:"buffer_frames"`
}

type VADConfig struct {
	Aggressiveness int      `yaml:"aggressiveness"`
	PreRoll        Duration `yaml:"pre_roll"`
	MinSpeech      Duration `yaml:"min_speech"`
	EndSilence     Duration `yaml:"end_silence"`
	MaxUtterance   Duration `yaml:"max_utterance"`
}

type STTConfig struct {
	URL              string   `yaml:"url"`
	Timeout          Duration `yaml:"timeout"`
	MaxResponseBytes int64    `yaml:"max_response_bytes"`
	APIKey           string   `yaml:"-"`
}

type LLMConfig struct {
	URL              string   `yaml:"url"`
	Model            string   `yaml:"model"`
	Temperature      float64  `yaml:"temperature"`
	MaxTokens        int      `yaml:"max_tokens"`
	Timeout          Duration `yaml:"timeout"`
	StreamIdle       Duration `yaml:"stream_idle_timeout"`
	MaxResponseBytes int64    `yaml:"max_response_bytes"`
	APIKey           string   `yaml:"-"`
}

type TTSConfig struct {
	URL              string   `yaml:"url"`
	Model            string   `yaml:"model"`
	Voice            string   `yaml:"voice"`
	Format           string   `yaml:"format"`
	Speed            float64  `yaml:"speed"`
	Timeout          Duration `yaml:"timeout"`
	QueueSize        int      `yaml:"queue_size"`
	MaxResponseBytes int64    `yaml:"max_response_bytes"`
	APIKey           string   `yaml:"-"`
}

type DialogueConfig struct {
	MaxHistoryMessages int `yaml:"max_history_messages"`
}

type TranscriptConfig struct {
	MinSignificantRunes int      `yaml:"min_significant_runes"`
	IgnoredExact        []string `yaml:"ignored_exact"`
	IgnoredPatterns     []string `yaml:"ignored_patterns"`
}

type WakeConfig struct {
	Phrases          []string `yaml:"phrases"`
	ActivationWindow Duration `yaml:"activation_window"`
}

type StorageConfig struct {
	ConversationsDir string `yaml:"conversations_dir"`
	RetentionDays    int    `yaml:"retention_days"`
}

type ControlConfig struct {
	PTTKey string `yaml:"ptt_key"`
}

func Default() Config {
	return Config{
		App: AppConfig{
			Mode:         ModeAlways,
			SystemPrompt: "Ты локальный голосовой ассистент. Отвечай на русском языке.",
			ResponsePolicy: "Отвечай кратко и естественно. Не озвучивай листинги кода и длинные " +
				"ссылки: сначала дай короткое устное объяснение.",
		},
		Log: LogConfig{Level: "info", Format: "text"},
		Audio: AudioConfig{
			SampleRate: 16000, Channels: 1, FrameMS: 20, BufferFrames: 64,
		},
		VAD: VADConfig{
			Aggressiveness: 2,
			PreRoll:        Duration(300 * time.Millisecond),
			MinSpeech:      Duration(250 * time.Millisecond),
			EndSilence:     Duration(600 * time.Millisecond),
			MaxUtterance:   Duration(30 * time.Second),
		},
		STT: STTConfig{
			URL: "http://127.0.0.1:8081/inference", Timeout: Duration(45 * time.Second),
			MaxResponseBytes: 1 << 20,
		},
		LLM: LLMConfig{
			URL: "http://127.0.0.1:1234/v1/chat/completions", Temperature: 0.4,
			MaxTokens: 512, Timeout: Duration(2 * time.Minute), StreamIdle: Duration(30 * time.Second),
			MaxResponseBytes: 4 << 20,
		},
		TTS: TTSConfig{
			URL: "http://127.0.0.1:8000/v1/audio/speech", Model: "tts-1",
			Voice: "ru_RU-dmitri-medium", Format: "wav", Speed: 1, Timeout: Duration(30 * time.Second),
			QueueSize: 8, MaxResponseBytes: 20 << 20,
		},
		Dialogue: DialogueConfig{MaxHistoryMessages: 20},
		Transcript: TranscriptConfig{
			MinSignificantRunes: 2,
			IgnoredExact:        []string{"а", "э", "ээ", "эм"},
			IgnoredPatterns:     []string{`^субтитры (сделал|создал).*$`},
		},
		Wake: WakeConfig{
			Phrases: []string{"ассистент"}, ActivationWindow: Duration(10 * time.Second),
		},
		Storage: StorageConfig{ConversationsDir: defaultConversationsDir(), RetentionDays: 30},
		Control: ControlConfig{PTTKey: "F8"},
	}
}

func Load(path string) (Config, error) {
	cfg := Default()
	if path != "" {
		if err := decodeFile(path, &cfg); err != nil {
			return Config{}, err
		}
	}
	if err := applyEnvironment(&cfg); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func decodeFile(path string, cfg *Config) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("открыть %q: %w", path, err)
	}
	defer file.Close()

	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(cfg); err != nil {
		return fmt.Errorf("прочитать YAML %q: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("прочитать YAML %q: разрешён один документ", path)
		}
		return fmt.Errorf("прочитать YAML %q: %w", path, err)
	}
	return nil
}

func applyEnvironment(cfg *Config) error {
	stringValues := []struct {
		name   string
		target *string
	}{
		{"ASSISTANT_MODE", &cfg.App.Mode},
		{"ASSISTANT_SYSTEM_PROMPT", &cfg.App.SystemPrompt},
		{"ASSISTANT_RESPONSE_POLICY", &cfg.App.ResponsePolicy},
		{"ASSISTANT_LOG_LEVEL", &cfg.Log.Level},
		{"ASSISTANT_LOG_FORMAT", &cfg.Log.Format},
		{"ASSISTANT_AUDIO_INPUT_DEVICE", &cfg.Audio.InputDevice},
		{"ASSISTANT_AUDIO_OUTPUT_DEVICE", &cfg.Audio.OutputDevice},
		{"ASSISTANT_STT_URL", &cfg.STT.URL},
		{"ASSISTANT_STT_API_KEY", &cfg.STT.APIKey},
		{"ASSISTANT_LLM_URL", &cfg.LLM.URL},
		{"ASSISTANT_LLM_MODEL", &cfg.LLM.Model},
		{"ASSISTANT_LLM_API_KEY", &cfg.LLM.APIKey},
		{"ASSISTANT_TTS_URL", &cfg.TTS.URL},
		{"ASSISTANT_TTS_MODEL", &cfg.TTS.Model},
		{"ASSISTANT_TTS_VOICE", &cfg.TTS.Voice},
		{"ASSISTANT_TTS_API_KEY", &cfg.TTS.APIKey},
		{"ASSISTANT_STORAGE_DIR", &cfg.Storage.ConversationsDir},
		{"ASSISTANT_PTT_KEY", &cfg.Control.PTTKey},
	}
	for _, value := range stringValues {
		if raw, ok := os.LookupEnv(value.name); ok {
			*value.target = raw
		}
	}

	setters := map[string]func(string) error{
		"ASSISTANT_AUDIO_SAMPLE_RATE":       intSetter(&cfg.Audio.SampleRate),
		"ASSISTANT_AUDIO_CHANNELS":          intSetter(&cfg.Audio.Channels),
		"ASSISTANT_AUDIO_FRAME_MS":          intSetter(&cfg.Audio.FrameMS),
		"ASSISTANT_AUDIO_BUFFER_FRAMES":     intSetter(&cfg.Audio.BufferFrames),
		"ASSISTANT_VAD_AGGRESSIVENESS":      intSetter(&cfg.VAD.Aggressiveness),
		"ASSISTANT_VAD_PRE_ROLL":            durationSetter(&cfg.VAD.PreRoll),
		"ASSISTANT_VAD_MIN_SPEECH":          durationSetter(&cfg.VAD.MinSpeech),
		"ASSISTANT_VAD_END_SILENCE":         durationSetter(&cfg.VAD.EndSilence),
		"ASSISTANT_VAD_MAX_UTTERANCE":       durationSetter(&cfg.VAD.MaxUtterance),
		"ASSISTANT_STT_TIMEOUT":             durationSetter(&cfg.STT.Timeout),
		"ASSISTANT_STT_MAX_RESPONSE_BYTES":  int64Setter(&cfg.STT.MaxResponseBytes),
		"ASSISTANT_LLM_TEMPERATURE":         floatSetter(&cfg.LLM.Temperature),
		"ASSISTANT_LLM_MAX_TOKENS":          intSetter(&cfg.LLM.MaxTokens),
		"ASSISTANT_LLM_TIMEOUT":             durationSetter(&cfg.LLM.Timeout),
		"ASSISTANT_LLM_STREAM_IDLE_TIMEOUT": durationSetter(&cfg.LLM.StreamIdle),
		"ASSISTANT_LLM_MAX_RESPONSE_BYTES":  int64Setter(&cfg.LLM.MaxResponseBytes),
		"ASSISTANT_TTS_SPEED":               floatSetter(&cfg.TTS.Speed),
		"ASSISTANT_TTS_TIMEOUT":             durationSetter(&cfg.TTS.Timeout),
		"ASSISTANT_TTS_QUEUE_SIZE":          intSetter(&cfg.TTS.QueueSize),
		"ASSISTANT_TTS_MAX_RESPONSE_BYTES":  int64Setter(&cfg.TTS.MaxResponseBytes),
		"ASSISTANT_MAX_HISTORY_MESSAGES":    intSetter(&cfg.Dialogue.MaxHistoryMessages),
		"ASSISTANT_TRANSCRIPT_MIN_RUNES":    intSetter(&cfg.Transcript.MinSignificantRunes),
		"ASSISTANT_WAKE_ACTIVATION_WINDOW":  durationSetter(&cfg.Wake.ActivationWindow),
		"ASSISTANT_RETENTION_DAYS":          intSetter(&cfg.Storage.RetentionDays),
	}
	for name, setter := range setters {
		if raw, ok := os.LookupEnv(name); ok {
			if err := setter(raw); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
		}
	}
	if raw, ok := os.LookupEnv("ASSISTANT_WAKE_PHRASES"); ok {
		cfg.Wake.Phrases = splitNonEmpty(raw)
	}
	if raw, ok := os.LookupEnv("ASSISTANT_TRANSCRIPT_IGNORED_EXACT"); ok {
		cfg.Transcript.IgnoredExact = splitNonEmpty(raw)
	}
	if raw, ok := os.LookupEnv("ASSISTANT_TRANSCRIPT_IGNORED_PATTERNS"); ok {
		cfg.Transcript.IgnoredPatterns = splitNonEmpty(raw)
	}
	return nil
}

func intSetter(target *int) func(string) error {
	return func(raw string) error {
		value, err := strconv.Atoi(raw)
		if err != nil {
			return err
		}
		*target = value
		return nil
	}
}

func int64Setter(target *int64) func(string) error {
	return func(raw string) error {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return err
		}
		*target = value
		return nil
	}
}

func floatSetter(target *float64) func(string) error {
	return func(raw string) error {
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return err
		}
		*target = value
		return nil
	}
}

func durationSetter(target *Duration) func(string) error {
	return func(raw string) error {
		return target.UnmarshalText([]byte(raw))
	}
}

func splitNonEmpty(raw string) []string {
	var values []string
	for _, value := range strings.Split(raw, ",") {
		if value = strings.TrimSpace(value); value != "" {
			values = append(values, value)
		}
	}
	return values
}

func (cfg Config) Validate() error {
	if cfg.App.Mode != ModeAlways && cfg.App.Mode != ModePTT && cfg.App.Mode != ModeWake {
		return fmt.Errorf("app.mode: неизвестный режим %q", cfg.App.Mode)
	}
	if strings.TrimSpace(cfg.App.SystemPrompt) == "" {
		return errors.New("app.system_prompt: значение обязательно")
	}
	if cfg.Log.Level != "debug" && cfg.Log.Level != "info" && cfg.Log.Level != "warn" && cfg.Log.Level != "error" {
		return fmt.Errorf("log.level: неизвестный уровень %q", cfg.Log.Level)
	}
	if cfg.Log.Format != "text" && cfg.Log.Format != "json" {
		return fmt.Errorf("log.format: неизвестный формат %q", cfg.Log.Format)
	}
	if cfg.Audio.SampleRate != 16000 || cfg.Audio.Channels != 1 {
		return errors.New("audio: VAD требует sample_rate=16000 и channels=1")
	}
	if cfg.Audio.FrameMS != 10 && cfg.Audio.FrameMS != 20 && cfg.Audio.FrameMS != 30 {
		return errors.New("audio.frame_ms: допустимы 10, 20 или 30")
	}
	if cfg.Audio.BufferFrames < 2 {
		return errors.New("audio.buffer_frames: значение должно быть не меньше 2")
	}
	if cfg.VAD.Aggressiveness < 0 || cfg.VAD.Aggressiveness > 3 {
		return errors.New("vad.aggressiveness: значение должно быть от 0 до 3")
	}
	if cfg.VAD.PreRoll < 0 || cfg.VAD.MinSpeech <= 0 || cfg.VAD.EndSilence <= 0 || cfg.VAD.MaxUtterance <= 0 {
		return errors.New("vad: интервалы должны быть положительными, pre_roll может быть равен 0")
	}
	if cfg.VAD.MaxUtterance <= cfg.VAD.MinSpeech {
		return errors.New("vad.max_utterance должен быть больше vad.min_speech")
	}
	for name, value := range map[string]string{"stt.url": cfg.STT.URL, "llm.url": cfg.LLM.URL, "tts.url": cfg.TTS.URL} {
		if err := validateServiceURL(value); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	if strings.TrimSpace(cfg.LLM.Model) == "" {
		return errors.New("llm.model: значение обязательно")
	}
	if cfg.LLM.Temperature < 0 || cfg.LLM.Temperature > 2 {
		return errors.New("llm.temperature: значение должно быть от 0 до 2")
	}
	if cfg.LLM.MaxTokens <= 0 {
		return errors.New("llm.max_tokens должен быть положительным")
	}
	if cfg.Dialogue.MaxHistoryMessages < 2 {
		return errors.New("dialogue.max_history_messages должен быть не меньше 2")
	}
	if cfg.Transcript.MinSignificantRunes <= 0 {
		return errors.New("transcript.min_significant_runes должен быть положительным")
	}
	for _, phrase := range cfg.Transcript.IgnoredExact {
		if strings.TrimSpace(phrase) == "" {
			return errors.New("transcript.ignored_exact не может содержать пустые фразы")
		}
	}
	for _, pattern := range cfg.Transcript.IgnoredPatterns {
		if _, err := regexp.Compile(pattern); err != nil {
			return fmt.Errorf("transcript.ignored_patterns: некорректный regexp %q: %w", pattern, err)
		}
	}
	if cfg.TTS.Model == "" || cfg.TTS.Voice == "" || cfg.TTS.Format != "wav" {
		return errors.New("tts: model и voice обязательны, format должен быть wav")
	}
	if cfg.TTS.Speed < 0.25 || cfg.TTS.Speed > 4 || cfg.TTS.QueueSize <= 0 {
		return errors.New("tts: speed должен быть от 0.25 до 4, queue_size должен быть положительным")
	}
	if len(cfg.Wake.Phrases) == 0 || cfg.Wake.ActivationWindow <= 0 {
		return errors.New("wake: нужна хотя бы одна фраза и положительное activation_window")
	}
	if cfg.Storage.RetentionDays < 0 || strings.TrimSpace(cfg.Storage.ConversationsDir) == "" {
		return errors.New("storage: retention_days не может быть отрицательным, conversations_dir обязателен")
	}
	if strings.TrimSpace(cfg.Control.PTTKey) == "" {
		return errors.New("control.ptt_key: значение обязательно")
	}
	return validateLimits(cfg)
}

func validateLimits(cfg Config) error {
	durations := map[string]Duration{
		"stt.timeout": cfg.STT.Timeout, "llm.timeout": cfg.LLM.Timeout,
		"llm.stream_idle_timeout": cfg.LLM.StreamIdle, "tts.timeout": cfg.TTS.Timeout,
	}
	for name, value := range durations {
		if value <= 0 {
			return fmt.Errorf("%s: значение должно быть положительным", name)
		}
	}
	limits := map[string]int64{
		"stt.max_response_bytes": cfg.STT.MaxResponseBytes,
		"llm.max_response_bytes": cfg.LLM.MaxResponseBytes,
		"tts.max_response_bytes": cfg.TTS.MaxResponseBytes,
	}
	for name, value := range limits {
		if value <= 0 {
			return fmt.Errorf("%s: значение должно быть положительным", name)
		}
	}
	return nil
}

func validateServiceURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("некорректный URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("разрешены только схемы http и https")
	}
	if parsed.Host == "" {
		return errors.New("host обязателен")
	}
	if parsed.User != nil {
		return errors.New("credentials внутри URL запрещены")
	}
	return nil
}

func defaultConversationsDir() string {
	dir, err := os.UserCacheDir()
	if err != nil || dir == "" {
		return filepath.Join("logs", "conversations")
	}
	return filepath.Join(dir, "VoiceAssistant", "conversations")
}
