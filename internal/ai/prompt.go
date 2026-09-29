package ai

import _ "embed"

// RouterSystemPrompt is the intent-routing system prompt, embedded so serve
// needs no disk assets. It must stay byte-identical with the eval corpus
// harness copy (evals/ai-router/router_system.md); TestPromptParity enforces
// it. Edit that file, then copy here.
//
//go:embed prompt_router.md
var RouterSystemPrompt string
