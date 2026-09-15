package provider

import (
	"encoding/json"

	"github.com/routatic/proxy/internal/config"
	"github.com/routatic/proxy/internal/core"
	"github.com/routatic/proxy/internal/transformer"
)

// anthropicVersion is the anthropic-version header every native /v1/messages
// request carries, as ogc sent it. OpenCode Go accepts its absence today, so
// only the handler test keeps it from being dropped again.
const anthropicVersion = "2023-06-01"

// anthropicRequestBody returns the body for a native /v1/messages request.
// When the handler kept the client's body, it is forwarded with only model and
// stream set. Rebuilding it from the normalized request drops fields the proxy
// has no model of (output_config, context_management, thinking.display) and
// turns Claude Code's adaptive thinking into {"type":"adaptive",
// "budget_tokens":0}, which OpenCode Go rejects.
func anthropicRequestBody(req *core.NormalizedRequest, model config.ModelConfig) ([]byte, error) {
	if len(req.RawBody) > 0 {
		return core.SetTopLevelFields(req.RawBody, map[string]any{"model": model.ModelID, "stream": req.Stream})
	}
	return json.Marshal(transformer.NormalizedToAnthropic(req, model))
}
