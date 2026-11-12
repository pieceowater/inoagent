package cfg

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/joho/godotenv"
)

// Config holds the configuration settings for the application. In production
// it comes from the .env the Ansible role renders
// (ansible/roles/inoagent/templates/env.j2 lists every variable).
type Config struct {
	ServiceName string // Used as the logger's service name
	LogLevel    string

	TelegramBotToken string
	// The one group the agent lives in. 0 = setup mode: the bot only answers
	// with chat and user ids, so they can be put into host_vars.
	TelegramChatID int64
	// Telegram user ids that may also talk to the bot in a private chat.
	// (Approval buttons can be pressed by any member of the group.)
	TelegramApproverIDs []int64
	AllowDM             bool
	// Which tool calls wait for a button: "off" (none — the agent just does
	// what it's asked), "danger" (only destructive ones), "all" (anything
	// that changes the system).
	ApprovalMode string
	// "all": answer every message in the group. "mention": only when
	// @mentioned or replied to.
	Trigger string

	MaxConcurrent   int // claude processes at once, across all topics
	DailyLimit      int // runs per day; 0 = no limit
	SessionIdle     time.Duration
	ApprovalTimeout time.Duration
	RunTimeout      time.Duration
	Debounce        time.Duration // messages of one person within this pause become one request; 0 = off
	CompactAt       int           // context size (tokens) at which a topic is compacted before the next request; 0 = never

	// Local Whisper service for voice messages; empty URL = no transcription.
	ASRURL      string
	ASRLanguage string

	ClaudeBin   string
	ClaudeModel string // empty: the CLI's own default
	// Set when CLAUDE_CODE_OAUTH_TOKEN or ANTHROPIC_API_KEY is present. The
	// values themselves are only read by the claude CLI, never by this code.
	HasClaudeAuth bool

	Workspace  string // the agent's cwd and memory (a git repo)
	StateDir   string // the bridge's own state.json
	HookSocket string // unix socket the PreToolUse hook talks to
	// BridgeSocket is where the owner's other Claude (on their own machine,
	// reaching the host over SSH) talks to the agent; BridgeThread is the
	// topic its requests and the answers are shown in.
	BridgeSocket string
	BridgeThread int
	BridgeTopic  string // or the topic by name, once the bot has seen a message in it
}

var (
	once     sync.Once
	instance *Config
)

// Inst returns a singleton instance of Config, loading environment variables if necessary.
func Inst() *Config {
	once.Do(func() {
		loadDotEnv()

		instance = &Config{
			ServiceName: getEnv("SERVICE_NAME", "ai.inoagent"),
			LogLevel:    getEnv("LOG_LEVEL", "info"),

			TelegramBotToken:    getEnv("TELEGRAM_BOT_TOKEN", ""),
			TelegramChatID:      getEnvInt64("TELEGRAM_CHAT_ID", 0),
			TelegramApproverIDs: getEnvIDs("TELEGRAM_APPROVER_IDS"),
			AllowDM:             getEnv("INOAGENT_ALLOW_DM", "true") == "true",
			Trigger:             getEnv("INOAGENT_TRIGGER", "all"),
			ApprovalMode:        getEnv("INOAGENT_APPROVAL_MODE", "off"),

			MaxConcurrent:   max(int(getEnvInt64("INOAGENT_MAX_CONCURRENT", 2)), 1),
			DailyLimit:      int(getEnvInt64("INOAGENT_DAILY_LIMIT", 0)),
			SessionIdle:     getEnvDuration("INOAGENT_SESSION_IDLE", 24*time.Hour),
			ApprovalTimeout: getEnvDuration("INOAGENT_APPROVAL_TIMEOUT", 10*time.Minute),
			RunTimeout:      getEnvDuration("INOAGENT_RUN_TIMEOUT", 30*time.Minute),
			Debounce:        getEnvDuration("INOAGENT_DEBOUNCE", 3*time.Second),
			CompactAt:       int(getEnvInt64("INOAGENT_COMPACT_AT", 80000)),

			ASRURL:      getEnv("INOAGENT_ASR_URL", ""),
			ASRLanguage: getEnv("INOAGENT_ASR_LANGUAGE", "ru"),

			ClaudeBin:     getEnv("INOAGENT_CLAUDE_BIN", "claude"),
			ClaudeModel:   getEnv("INOAGENT_MODEL", ""),
			HasClaudeAuth: os.Getenv("CLAUDE_CODE_OAUTH_TOKEN") != "" || os.Getenv("ANTHROPIC_API_KEY") != "",

			Workspace:  getEnv("INOAGENT_WORKSPACE", "/workspace"),
			StateDir:   getEnv("INOAGENT_STATE_DIR", "/state"),
			HookSocket: getEnv("INOAGENT_HOOK_SOCK", "/tmp/inoagent-hook.sock"),

			BridgeSocket: getEnv("INOAGENT_BRIDGE_SOCK", "/state/bridge.sock"),
			BridgeThread: int(getEnvInt64("INOAGENT_BRIDGE_THREAD", 0)),
			BridgeTopic:  getEnv("INOAGENT_BRIDGE_TOPIC", "Мост"),
		}
	})
	return instance
}

// Validate reports settings the service can't start with.
func (c *Config) Validate() error {
	if c.TelegramBotToken == "" {
		return fmt.Errorf("TELEGRAM_BOT_TOKEN is required")
	}
	switch c.ApprovalMode {
	case "off", "danger", "all":
	default:
		return fmt.Errorf("INOAGENT_APPROVAL_MODE must be off, danger or all, got %q", c.ApprovalMode)
	}
	if c.Trigger != "all" && c.Trigger != "mention" {
		return fmt.Errorf("INOAGENT_TRIGGER must be all or mention, got %q", c.Trigger)
	}
	return nil
}

// SetupMode: no group configured yet, so the bot only tells people their ids.
func (c *Config) SetupMode() bool { return c.TelegramChatID == 0 }

func (c *Config) IsApprover(id int64) bool {
	for _, a := range c.TelegramApproverIDs {
		if a == id {
			return true
		}
	}
	return false
}

func loadDotEnv() {
	if err := godotenv.Load(); err == nil {
		return
	}

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		return
	}

	projectRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "../../../"))
	_ = godotenv.Load(filepath.Join(projectRoot, ".env"))
}

// getEnv retrieves the value of the environment variable named by the key.
// It returns the value, or the specified default value if the variable is not present.
func getEnv(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists && value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt64(key string, defaultValue int64) int64 {
	if value, exists := os.LookupEnv(key); exists {
		if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
			return parsed
		}
	}
	return defaultValue
}

func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if value, exists := os.LookupEnv(key); exists {
		if parsed, err := time.ParseDuration(value); err == nil {
			return parsed
		}
	}
	return defaultValue
}

// getEnvIDs reads a comma- or space-separated list of Telegram ids; anything
// that isn't a number is skipped.
func getEnvIDs(key string) []int64 {
	var ids []int64
	for _, s := range strings.FieldsFunc(os.Getenv(key), func(r rune) bool { return r == ',' || r == ' ' }) {
		if id, err := strconv.ParseInt(s, 10, 64); err == nil {
			ids = append(ids, id)
		}
	}
	return ids
}
