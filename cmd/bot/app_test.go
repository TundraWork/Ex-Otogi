package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	driverpkg "ex-otogi/internal/driver"
	"ex-otogi/internal/kernel"
	llmconfig "ex-otogi/pkg/llm/config"
	"ex-otogi/pkg/otogi"
)

func writeConfigFile(t *testing.T, path string, contents string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config file: %v", err)
	}
}

func writeLLMConfigFile(t *testing.T, path string) {
	t.Helper()

	writeConfigFile(t, path, `{
		"request_timeout":"30s",
		"providers":{
			"openai-main":{
				"type":"openai",
				"api_key":"sk-test",
				"base_url":"https://api.openai.com/v1",
				"openai":{"max_retries":2}
			}
		},
		"agents":[
			{
				"name":"Otogi",
				"description":"Assistant",
				"provider":"openai-main",
				"model":"gpt-5-mini",
				"system_prompt_template":"You are {{.AgentName}}",
				"request_timeout":"30s"
			}
		]
	}`)
}

func mustBuiltinDriverRegistry(t *testing.T) *driverpkg.Registry {
	t.Helper()

	registry, err := driverpkg.NewBuiltinRegistry()
	if err != nil {
		t.Fatalf("new builtin registry failed: %v", err)
	}

	return registry
}

func TestParseLogLevel(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    slog.Level
		wantErr bool
	}{
		{name: "debug", input: "debug", want: slog.LevelDebug},
		{name: "info", input: "info", want: slog.LevelInfo},
		{name: "warn", input: "warn", want: slog.LevelWarn},
		{name: "warning", input: "warning", want: slog.LevelWarn},
		{name: "error", input: "error", want: slog.LevelError},
		{name: "invalid", input: "trace", wantErr: true},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			got, err := parseLogLevel(testCase.input)
			if testCase.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !testCase.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if testCase.wantErr {
				return
			}
			if got != testCase.want {
				t.Fatalf("level = %v, want %v", got, testCase.want)
			}
		})
	}
}

func TestLoadConfig(t *testing.T) {
	t.Run("loads drivers and routing config", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "bot.json")
		writeConfigFile(t, configPath, `{
			"log_level":"warn",
			"kernel":{
				"module_lifecycle_timeout":"7s",
				"module_handler_timeout":"11s",
				"shutdown_timeout":"15s",
				"subscription_buffer":64,
				"subscription_workers":5
			},
			"drivers":[
				{
					"name":"tg-main",
					"type":"telegram",
					"enabled":true,
					"config":{
						"app_id":123456,
						"app_hash":"sample_hash"
					}
				}
			],
			"routing":{
				"default":{
					"sources":[{"platform":"telegram","id":"tg-main"}],
					"sink":{"platform":"telegram","id":"tg-main"}
				}
			}
		}`)
		t.Setenv(envConfigFile, configPath)

		cfg, err := loadConfig(mustBuiltinDriverRegistry(t))
		if err != nil {
			t.Fatalf("load config failed: %v", err)
		}

		if cfg.logLevel != slog.LevelWarn {
			t.Fatalf("log level = %v, want %v", cfg.logLevel, slog.LevelWarn)
		}
		if cfg.moduleLifecycleTimeout != 7*time.Second {
			t.Fatalf("module lifecycle timeout = %s, want 7s", cfg.moduleLifecycleTimeout)
		}
		if cfg.moduleHandlerTimeout != 11*time.Second {
			t.Fatalf("module handler timeout = %s, want 11s", cfg.moduleHandlerTimeout)
		}
		if cfg.shutdownTimeout != 15*time.Second {
			t.Fatalf("shutdown timeout = %s, want 15s", cfg.shutdownTimeout)
		}
		if cfg.subscriptionBuffer != 64 {
			t.Fatalf("subscription buffer = %d, want 64", cfg.subscriptionBuffer)
		}
		if cfg.subscriptionWorkers != 5 {
			t.Fatalf("subscription workers = %d, want 5", cfg.subscriptionWorkers)
		}
		if len(cfg.drivers) != 1 {
			t.Fatalf("drivers len = %d, want 1", len(cfg.drivers))
		}
		if cfg.drivers[0].Name != "tg-main" {
			t.Fatalf("driver name = %q, want tg-main", cfg.drivers[0].Name)
		}
		if cfg.routingDefault == nil || cfg.routingDefault.Sink == nil {
			t.Fatal("expected routing default sink")
		}
		if cfg.routingDefault.Sink.ID != "tg-main" {
			t.Fatalf("default sink id = %q, want tg-main", cfg.routingDefault.Sink.ID)
		}
	})

	t.Run("single-driver mode infers default route", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "bot.json")
		writeConfigFile(t, configPath, `{
			"drivers":[
				{
					"name":"tg-main",
					"type":"telegram",
					"config":{"app_id":123456,"app_hash":"sample_hash"}
				}
			]
		}`)
		t.Setenv(envConfigFile, configPath)

		cfg, err := loadConfig(mustBuiltinDriverRegistry(t))
		if err != nil {
			t.Fatalf("load config failed: %v", err)
		}
		if cfg.routingDefault == nil || cfg.routingDefault.Sink == nil {
			t.Fatal("expected inferred default sink route")
		}
		if cfg.routingDefault.Sink.ID != "tg-main" {
			t.Fatalf("default sink id = %q, want tg-main", cfg.routingDefault.Sink.ID)
		}
	})

	t.Run("loads fallback path bin/config/bot.json when no explicit path is set", func(t *testing.T) {
		workDir := t.TempDir()
		configPath := filepath.Join(workDir, "bin", "config", "bot.json")
		writeConfigFile(t, configPath, `{
			"drivers":[
				{"name":"tg-main","type":"telegram","config":{"app_id":1,"app_hash":"hash"}}
			]
		}`)

		currentDir, err := os.Getwd()
		if err != nil {
			t.Fatalf("get working directory: %v", err)
		}
		if err := os.Chdir(workDir); err != nil {
			t.Fatalf("chdir to temp work dir: %v", err)
		}
		t.Cleanup(func() {
			if err := os.Chdir(currentDir); err != nil {
				t.Fatalf("restore working directory: %v", err)
			}
		})
		t.Setenv(envConfigFile, "")

		cfg, err := loadConfig(mustBuiltinDriverRegistry(t))
		if err != nil {
			t.Fatalf("load config failed: %v", err)
		}
		if len(cfg.drivers) != 1 || cfg.drivers[0].Name != "tg-main" {
			t.Fatalf("unexpected drivers: %+v", cfg.drivers)
		}
	})

	t.Run("invalid config values fail", func(t *testing.T) {
		tests := []struct {
			name       string
			fileJSON   string
			wantErrSub string
		}{
			{
				name:       "invalid log level",
				fileJSON:   `{"log_level":"trace","drivers":[{"name":"tg","type":"telegram","config":{"app_id":1,"app_hash":"hash"}}]}`,
				wantErrSub: "parse log_level",
			},
			{
				name:       "invalid lifecycle timeout",
				fileJSON:   `{"kernel":{"module_lifecycle_timeout":"bad"},"drivers":[{"name":"tg","type":"telegram","config":{"app_id":1,"app_hash":"hash"}}]}`,
				wantErrSub: "parse kernel.module_lifecycle_timeout",
			},
			{
				name:       "invalid handler timeout",
				fileJSON:   `{"kernel":{"module_handler_timeout":"0s"},"drivers":[{"name":"tg","type":"telegram","config":{"app_id":1,"app_hash":"hash"}}]}`,
				wantErrSub: "parse kernel.module_handler_timeout",
			},
			{
				name:       "legacy hook timeout key rejected",
				fileJSON:   `{"kernel":{"module_hook_timeout":"7s"},"drivers":[{"name":"tg","type":"telegram","config":{"app_id":1,"app_hash":"hash"}}]}`,
				wantErrSub: "renamed to kernel.module_lifecycle_timeout",
			},
			{
				name:       "missing drivers",
				fileJSON:   `{}`,
				wantErrSub: "at least one enabled driver",
			},
			{
				name:       "legacy telegram section fails with missing drivers",
				fileJSON:   `{"telegram":{"app_id":1,"app_hash":"hash"}}`,
				wantErrSub: "at least one enabled driver",
			},
			{
				name:       "unsupported driver type",
				fileJSON:   `{"drivers":[{"name":"legacy","type":"discord","config":{"token":"x"}}]}`,
				wantErrSub: "unsupported type discord",
			},
		}

		for _, testCase := range tests {
			testCase := testCase
			t.Run(testCase.name, func(t *testing.T) {
				configPath := filepath.Join(t.TempDir(), "bot.json")
				writeConfigFile(t, configPath, testCase.fileJSON)
				t.Setenv(envConfigFile, configPath)

				_, err := loadConfig(mustBuiltinDriverRegistry(t))
				if err == nil {
					t.Fatal("expected error")
				}
				if !strings.Contains(err.Error(), testCase.wantErrSub) {
					t.Fatalf("error = %v, want substring %q", err, testCase.wantErrSub)
				}
			})
		}
	})

	t.Run("multi-driver requires default route unless all modules override", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "bot.json")
		writeConfigFile(t, configPath, `{
			"drivers":[
				{"name":"tg-main","type":"telegram","config":{"app_id":1,"app_hash":"hash"}},
				{"name":"tg-alt","type":"telegram","config":{"app_id":2,"app_hash":"hash2"}}
			]
		}`)
		t.Setenv(envConfigFile, configPath)

		_, err := loadConfig(mustBuiltinDriverRegistry(t))
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "routing.default is required") {
			t.Fatalf("error = %v, want routing.default error", err)
		}
	})

	t.Run("missing explicit config file fails", func(t *testing.T) {
		t.Setenv(envConfigFile, filepath.Join(t.TempDir(), "missing.json"))
		if _, err := loadConfig(mustBuiltinDriverRegistry(t)); err == nil {
			t.Fatal("expected error for missing config file")
		}
	})

	t.Run("llm config file from bot config enables llm module", func(t *testing.T) {
		rootDir := t.TempDir()
		llmConfigPath := filepath.Join(rootDir, "llm.json")
		writeLLMConfigFile(t, llmConfigPath)

		configPath := filepath.Join(rootDir, "bot.json")
		writeConfigFile(t, configPath, `{
			"drivers":[
				{
					"name":"tg-main",
					"type":"telegram",
					"config":{"app_id":123456,"app_hash":"sample_hash"}
				}
			],
			"llm":{
				"config_file":"`+llmConfigPath+`"
			}
		}`)
		t.Setenv(envConfigFile, configPath)

		cfg, err := loadConfig(mustBuiltinDriverRegistry(t))
		if err != nil {
			t.Fatalf("load config failed: %v", err)
		}
		if cfg.llmConfig == nil {
			t.Fatal("expected llm config to be loaded")
		}
		if cfg.llmConfigFile != llmConfigPath {
			t.Fatalf("llm config file = %q, want %q", cfg.llmConfigFile, llmConfigPath)
		}

		moduleNames := configuredRuntimeModuleNames(&cfg)
		found := false
		for _, moduleName := range moduleNames {
			if moduleName == "llmchat" {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("configured modules = %v, want llmchat included", moduleNames)
		}
	})

	t.Run("llm config env override takes precedence", func(t *testing.T) {
		rootDir := t.TempDir()
		llmConfigFromFile := filepath.Join(rootDir, "llm-file.json")
		llmConfigFromEnv := filepath.Join(rootDir, "llm-env.json")
		writeLLMConfigFile(t, llmConfigFromFile)
		writeLLMConfigFile(t, llmConfigFromEnv)

		configPath := filepath.Join(rootDir, "bot.json")
		writeConfigFile(t, configPath, `{
			"drivers":[
				{
					"name":"tg-main",
					"type":"telegram",
					"config":{"app_id":123456,"app_hash":"sample_hash"}
				}
			],
			"llm":{
				"config_file":"`+llmConfigFromFile+`"
			}
		}`)
		t.Setenv(envConfigFile, configPath)
		t.Setenv(envLLMConfigFile, llmConfigFromEnv)

		cfg, err := loadConfig(mustBuiltinDriverRegistry(t))
		if err != nil {
			t.Fatalf("load config failed: %v", err)
		}
		if cfg.llmConfigFile != llmConfigFromEnv {
			t.Fatalf("llm config file = %q, want env override %q", cfg.llmConfigFile, llmConfigFromEnv)
		}
	})

	t.Run("configured llm path missing fails fast", func(t *testing.T) {
		rootDir := t.TempDir()
		configPath := filepath.Join(rootDir, "bot.json")
		missingLLMConfigPath := filepath.Join(rootDir, "missing-llm.json")
		writeConfigFile(t, configPath, `{
			"drivers":[
				{
					"name":"tg-main",
					"type":"telegram",
					"config":{"app_id":123456,"app_hash":"sample_hash"}
				}
			],
			"llm":{
				"config_file":"`+missingLLMConfigPath+`"
			}
		}`)
		t.Setenv(envConfigFile, configPath)

		_, err := loadConfig(mustBuiltinDriverRegistry(t))
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "load llm config file") {
			t.Fatalf("error = %v, want llm config load failure", err)
		}
	})

	t.Run("invalid llm provider profile fails fast", func(t *testing.T) {
		rootDir := t.TempDir()
		llmConfigPath := filepath.Join(rootDir, "llm-invalid.json")
		writeConfigFile(t, llmConfigPath, `{
			"providers":{
				"openai-main":{"type":"openai","api_key":""}
			},
			"agents":[
				{
					"name":"Otogi",
					"description":"Assistant",
					"provider":"openai-main",
					"model":"gpt-5-mini",
					"system_prompt_template":"You are {{.AgentName}}"
				}
			]
		}`)

		configPath := filepath.Join(rootDir, "bot.json")
		writeConfigFile(t, configPath, `{
			"drivers":[
				{
					"name":"tg-main",
					"type":"telegram",
					"config":{"app_id":123456,"app_hash":"sample_hash"}
				}
			],
			"llm":{
				"config_file":"`+llmConfigPath+`"
			}
		}`)
		t.Setenv(envConfigFile, configPath)

		_, err := loadConfig(mustBuiltinDriverRegistry(t))
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "load llm config file") {
			t.Fatalf("error = %v, want llm config load failure", err)
		}
	})
}

func TestRegisterRuntimeServicesLLMRegistry(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("llm disabled does not register provider registry", func(t *testing.T) {
		kernelRuntime := kernel.New()
		err := registerRuntimeServices(kernelRuntime, logger, &sinkDispatcherTestStub{}, appConfig{})
		if err != nil {
			t.Fatalf("registerRuntimeServices failed: %v", err)
		}

		_, resolveErr := kernelRuntime.Services().Resolve(otogi.ServiceLLMProviderRegistry)
		if !errors.Is(resolveErr, otogi.ErrServiceNotFound) {
			t.Fatalf("resolve provider registry error = %v, want ErrServiceNotFound", resolveErr)
		}
	})

	t.Run("llm enabled registers provider registry with multiple profiles", func(t *testing.T) {
		kernelRuntime := kernel.New()
		cfg := appConfig{
			llmConfig: &llmconfig.Config{
				RequestTimeout: time.Second,
				Providers: map[string]llmconfig.ProviderProfile{
					"openai-main": {
						Type:   "openai",
						APIKey: "sk-main",
					},
					"gemini-main": {
						Type:   "gemini",
						APIKey: "gm-main",
						Gemini: &llmconfig.GeminiOptions{
							RequestDefaults: llmconfig.GeminiRequestDefaults{
								GoogleSearch: ptrBool(true),
							},
						},
					},
				},
				Agents: []llmconfig.Agent{
					{
						Name:                 "Otogi",
						Description:          "Primary",
						Provider:             "openai-main",
						Model:                "gpt-5-mini",
						SystemPromptTemplate: "You are {{.AgentName}}",
						RequestTimeout:       time.Second,
					},
					{
						Name:                 "OtogiGemini",
						Description:          "Gemini",
						Provider:             "gemini-main",
						Model:                "gemini-2.5-flash",
						SystemPromptTemplate: "You are {{.AgentName}}",
						RequestTimeout:       time.Second,
					},
				},
			},
		}

		err := registerRuntimeServices(kernelRuntime, logger, &sinkDispatcherTestStub{}, cfg)
		if err != nil {
			t.Fatalf("registerRuntimeServices failed: %v", err)
		}

		resolved, err := kernelRuntime.Services().Resolve(otogi.ServiceLLMProviderRegistry)
		if err != nil {
			t.Fatalf("resolve provider registry failed: %v", err)
		}
		registry, ok := resolved.(otogi.LLMProviderRegistry)
		if !ok {
			t.Fatalf("resolved registry type = %T, want otogi.LLMProviderRegistry", resolved)
		}
		if _, err := registry.Resolve("openai-main"); err != nil {
			t.Fatalf("resolve openai-main failed: %v", err)
		}
		if _, err := registry.Resolve("gemini-main"); err != nil {
			t.Fatalf("resolve gemini-main failed: %v", err)
		}
	})

	t.Run("invalid provider config fails fast during service registration", func(t *testing.T) {
		kernelRuntime := kernel.New()
		cfg := appConfig{
			llmConfig: &llmconfig.Config{
				RequestTimeout: time.Second,
				Providers: map[string]llmconfig.ProviderProfile{
					"openai-main": {
						Type:   "openai",
						APIKey: "",
					},
				},
				Agents: []llmconfig.Agent{
					{
						Name:                 "Otogi",
						Description:          "Primary",
						Provider:             "openai-main",
						Model:                "gpt-5-mini",
						SystemPromptTemplate: "You are {{.AgentName}}",
						RequestTimeout:       time.Second,
					},
				},
			},
		}

		err := registerRuntimeServices(kernelRuntime, logger, &sinkDispatcherTestStub{}, cfg)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "build llm providers") {
			t.Fatalf("error = %v, want build llm providers failure", err)
		}
	})
}

func TestConfigureStdlibLogBridge(t *testing.T) {
	var buffer bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buffer, nil))

	previousWriter := log.Writer()
	previousFlags := log.Flags()
	t.Cleanup(func() {
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
	})

	configureStdlibLogBridge(logger)
	log.Println("Error context canceled")

	output := buffer.String()
	if output == "" {
		t.Fatal("expected bridged slog output")
	}
	if !strings.Contains(output, `"level":"WARN"`) {
		t.Fatalf("output = %q, want level WARN", output)
	}
	if !strings.Contains(output, `"msg":"Error context canceled"`) {
		t.Fatalf("output = %q, want bridged message", output)
	}
	if !strings.Contains(output, `"source":"stdlib"`) {
		t.Fatalf("output = %q, want source=stdlib", output)
	}
	if !strings.Contains(output, `"component":"google_genai_sdk"`) {
		t.Fatalf("output = %q, want component=google_genai_sdk", output)
	}
	if !strings.Contains(output, `"sdk_log_kind":"context_canceled"`) {
		t.Fatalf("output = %q, want sdk_log_kind=context_canceled", output)
	}
}

func TestConfigureStdlibLogBridgeKeepsOtherLogsAsError(t *testing.T) {
	var buffer bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buffer, nil))

	previousWriter := log.Writer()
	previousFlags := log.Flags()
	t.Cleanup(func() {
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
	})

	configureStdlibLogBridge(logger)
	log.Println("Some other sdk failure")

	output := buffer.String()
	if output == "" {
		t.Fatal("expected bridged slog output")
	}
	if !strings.Contains(output, `"level":"ERROR"`) {
		t.Fatalf("output = %q, want level ERROR", output)
	}
	if strings.Contains(output, `"sdk_log_kind":"context_canceled"`) {
		t.Fatalf("output = %q, should not include context-canceled log kind", output)
	}
}

type sinkDispatcherTestStub struct{}

func ptrBool(value bool) *bool {
	return &value
}

func (*sinkDispatcherTestStub) SendMessage(context.Context, otogi.SendMessageRequest) (*otogi.OutboundMessage, error) {
	return &otogi.OutboundMessage{ID: "msg-1"}, nil
}

func (*sinkDispatcherTestStub) EditMessage(context.Context, otogi.EditMessageRequest) error {
	return nil
}

func (*sinkDispatcherTestStub) DeleteMessage(context.Context, otogi.DeleteMessageRequest) error {
	return nil
}

func (*sinkDispatcherTestStub) SetReaction(context.Context, otogi.SetReactionRequest) error {
	return nil
}

func (*sinkDispatcherTestStub) ListSinks(context.Context) ([]otogi.EventSink, error) {
	return nil, nil
}

func (*sinkDispatcherTestStub) ListSinksByPlatform(context.Context, otogi.Platform) ([]otogi.EventSink, error) {
	return nil, nil
}
