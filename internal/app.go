package internal

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"app/internal/core/cfg"
	"app/internal/core/generic/format"
	"app/internal/core/messenger"
	"app/internal/core/observability"
	"app/internal/core/telegram"
	"app/internal/pkg"
	agentCtrl "app/internal/pkg/agent/ctrl"
	"app/internal/pkg/agent/repo"
)

type Application interface {
	Start()
	Stop()
}

type App struct {
	cfg    *cfg.Config
	ctx    context.Context
	cancel context.CancelFunc
	logger *slog.Logger
	tg     *telegram.Client
	msg    *messenger.Messenger
	repo   *repo.Repo
	hook   *http.Server
	bridge *http.Server

	lastPoll atomic.Int64 // unix seconds of the last successful getUpdates
	done     sync.WaitGroup
}

func NewApp() *App {
	c := cfg.Inst()
	logger := observability.Init(observability.Config{ServiceName: c.ServiceName, LogLevel: c.LogLevel})
	if err := c.Validate(); err != nil {
		logger.Error("bad configuration", "err", err)
		os.Exit(1)
	}

	repository, err := repo.New(c.StateDir)
	if err != nil {
		logger.Error("failed to open state", "dir", c.StateDir, "err", err)
		os.Exit(1)
	}

	tg := telegram.New(c.TelegramBotToken)
	// Known secret values are scrubbed from everything the bot sends — a
	// last line of defence if a command ever prints one.
	secrets := []string{c.TelegramBotToken, os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"), os.Getenv("ANTHROPIC_API_KEY")}
	// Credentials the owner handed the agent (inoagent_secret_env).
	for _, name := range strings.Split(os.Getenv("INOAGENT_SECRET_ENV"), ",") {
		if name = strings.TrimSpace(name); name != "" {
			secrets = append(secrets, os.Getenv(name))
		}
	}
	redactor := format.NewRedactor(secrets...)

	ctx, cancel := context.WithCancel(context.Background())
	return &App{
		cfg:    c,
		ctx:    ctx,
		cancel: cancel,
		logger: logger,
		tg:     tg,
		msg:    messenger.New(tg, redactor, logger),
		repo:   repository,
	}
}

func (a *App) Start() {
	me, err := a.getMe()
	if err != nil {
		a.logger.Error("telegram getMe failed", "err", err)
		os.Exit(1)
	}
	a.logger.Info("telegram connected", "bot", me.Username, "chat_id", a.cfg.TelegramChatID, "setup_mode", a.cfg.SetupMode())
	if !a.cfg.HasClaudeAuth {
		a.logger.Warn("no CLAUDE_CODE_OAUTH_TOKEN: the bot answers, but cannot think until it is set")
	}
	_ = a.tg.SetMyCommands(a.ctx, agentCtrl.Commands)

	router := pkg.NewRouter(a.ctx, a.repo, a.msg, me, a.logger)
	if _, err := router.InitializeRouter(); err != nil {
		a.logger.Error("failed to initialize modules", "err", err)
		os.Exit(1)
	}
	if err := a.serveHook(router); err != nil {
		a.logger.Error("failed to open hook socket", "sock", a.cfg.HookSocket, "err", err)
		os.Exit(1)
	}

	if err := a.serveBridge(router); err != nil {
		a.logger.Warn("bridge socket not opened", "sock", a.cfg.BridgeSocket, "err", err)
	}

	a.done.Add(1)
	go func() {
		defer a.done.Done()
		a.poll(router)
	}()
}

func (a *App) Stop() {
	a.cancel()
	if a.hook != nil {
		_ = a.hook.Close()
	}
	if a.bridge != nil {
		_ = a.bridge.Close()
	}
	a.done.Wait()
}

// getMe retries for a while: on boot the network (or Tailscale's DNS) may not
// be up yet, and a crash loop would only hide the real cause.
func (a *App) getMe() (telegram.User, error) {
	var err error
	for i := 0; i < 10; i++ {
		var me telegram.User
		if me, err = a.tg.GetMe(a.ctx); err == nil {
			return me, nil
		}
		time.Sleep(3 * time.Second)
	}
	return telegram.User{}, err
}

// poll is the Telegram long-poll loop. Long polling is outbound-only, which is
// why the bot works on a server with no public IP.
func (a *App) poll(router *pkg.Router) {
	offset := a.repo.Offset()
	backoff := time.Second
	for a.ctx.Err() == nil {
		updates, err := a.tg.GetUpdates(a.ctx, offset, 50)
		if err != nil {
			if a.ctx.Err() != nil {
				return
			}
			a.logger.Warn("getUpdates failed", "err", err, "retry_in", backoff)
			a.tg.ResetConnections()
			select {
			case <-time.After(backoff):
			case <-a.ctx.Done():
			}
			backoff = min(backoff*2, 30*time.Second)
			continue
		}
		backoff = time.Second
		a.lastPoll.Store(time.Now().Unix())
		for _, u := range updates {
			offset = u.UpdateID + 1
			router.Route(a.ctx, u)
		}
		if err := a.repo.SetOffset(offset); err != nil {
			a.logger.Error("save offset", "err", err)
		}
	}
}

// serveHook opens the unix socket cmd/hook and cmd/healthcheck talk to.
func (a *App) serveHook(router *pkg.Router) error {
	sock := a.cfg.HookSocket
	_ = os.Remove(sock) // a stale socket from the previous run
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	if err := os.Chmod(sock, 0o600); err != nil {
		return err
	}
	mux := http.NewServeMux()
	router.HookRoutes(mux)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		// Healthy = Telegram answered recently (a long poll takes up to 50s).
		if time.Since(time.Unix(a.lastPoll.Load(), 0)) > 3*time.Minute {
			http.Error(w, "telegram is not answering", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
	a.hook = &http.Server{Handler: mux}
	go func() {
		if err := a.hook.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			a.logger.Error("hook server stopped", "err", err)
		}
	}()
	return nil
}

// serveBridge opens the socket for the owner's Claude on their own machine.
// It lives in the state directory, which only the host's owner can read, so
// getting to it takes an SSH login.
func (a *App) serveBridge(router *pkg.Router) error {
	if a.cfg.BridgeSocket == "" || a.cfg.SetupMode() {
		return nil
	}
	_ = os.Remove(a.cfg.BridgeSocket)
	ln, err := net.Listen("unix", a.cfg.BridgeSocket)
	if err != nil {
		return err
	}
	if err := os.Chmod(a.cfg.BridgeSocket, 0o600); err != nil {
		return err
	}
	mux := http.NewServeMux()
	router.BridgeRoutes(mux)
	a.bridge = &http.Server{Handler: mux}
	go func() {
		if err := a.bridge.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			a.logger.Error("bridge server stopped", "err", err)
		}
	}()
	return nil
}
