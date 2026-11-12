package ctrl

import (
	"encoding/json"
	"net/http"

	"app/internal/pkg/agent/ent"
)

// BridgeRoutes serves the owner's other Claude. The socket is only reachable
// by logging in to the host over SSH, which is the whole access control.
func (c *AgentController) BridgeRoutes(mux *http.ServeMux) {
	// POST /ask {"text": "..."} → {"answer": "..."} once the agent is done.
	mux.HandleFunc("POST /ask", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		answer, err := c.svc.Ask(r.Context(), req.Text)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"answer": answer})
	})
	// GET /status → what the agent is doing in the bridge topic.
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, _ *http.Request) {
		key := ent.TopicKey{Chat: c.cfg.TelegramChatID, Thread: c.svc.BridgeThread()}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"busy":     c.svc.TopicStatus(key).Busy,
			"queued":   c.svc.TopicStatus(key).Queued,
			"progress": progressText(c.svc.Progress(key)),
		})
	})
}
