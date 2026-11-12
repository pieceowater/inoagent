package svc

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"app/internal/core/telegram"
)

// Self-deploy hand-off. The `self-deploy` helper (deploy/self-deploy) writes
// deploy-target.json — which topic asked — and schedules
// deploy/self-deploy.sh on the host. That script rebuilds and recreates this
// container, then writes deploy-result.json. Whichever container is running by
// then (the new one, or the old one if the build failed) finds the result
// here and reports it to the topic.
const (
	deployTarget = "deploy-target.json"
	deployResult = "deploy-result.json"
)

type deployTargetFile struct {
	Chat    int64 `json:"chat"`
	Thread  int   `json:"thread"`
	ReplyTo int   `json:"reply_to"`
}

type deployResultFile struct {
	OK     bool   `json:"ok"`
	Commit string `json:"commit"`
	Log    string `json:"log"`
}

// WatchDeploys polls for a finished self-deploy until the service stops.
func (s *AgentService) WatchDeploys() {
	go func() {
		tick := time.NewTicker(5 * time.Second)
		defer tick.Stop()
		for {
			s.reportDeploy()
			select {
			case <-s.ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
}

func (s *AgentService) reportDeploy() {
	resultPath := filepath.Join(s.cfg.StateDir, deployResult)
	b, err := os.ReadFile(resultPath)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	targetPath := filepath.Join(s.cfg.StateDir, deployTarget)
	defer os.Remove(resultPath)
	defer os.Remove(targetPath)

	var res deployResultFile
	if err != nil || json.Unmarshal(b, &res) != nil {
		s.log.Error("unreadable deploy result", "err", err)
		return
	}
	s.log.Info("self-deploy finished", "ok", res.OK, "commit", res.Commit)
	var t deployTargetFile
	if tb, err := os.ReadFile(targetPath); err != nil || json.Unmarshal(tb, &t) != nil || t.Chat == 0 {
		return // nobody to tell: a deploy started by hand
	}
	to := &telegram.Message{
		MessageID:       t.ReplyTo,
		MessageThreadID: t.Thread,
		IsTopicMessage:  t.Thread != 0,
		Chat:            telegram.Chat{ID: t.Chat},
	}
	if res.OK {
		s.msg.Reply(s.ctx, to, "🧬 Я эволюционировал, теперь я на `"+res.Commit+"`")
		return
	}
	s.msg.Reply(s.ctx, to, "💥 сборка упала, работаю на старой версии (`"+res.Commit+"`):\n```\n"+truncate(res.Log, 3000)+"\n```")
}
