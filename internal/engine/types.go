package engine

type Workflow struct {
	ID        string          `json:"id"`
	StartNode string          `json:"start_node"`
	Steps     map[string]Step `json:"steps"`
}

type Step struct {
	Type   string            `json:"type"` // play_audio, execute_command, wait, handoff
	Params map[string]string `json:"params"`
	Next   string            `json:"next,omitempty"`
}

type Session struct {
	CallID      string
	WorkflowID  string
	CurrentStep string
	Variables   map[string]interface{}
}
