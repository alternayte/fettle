// Package teach keeps teach sessions (§13.3): a person does a task once on a bot's
// screen, Fettle records the steps and notes, the bot asks its questions, and a draft
// skill waits for review.
package teach

import (
	"github.com/alternayte/drel"
	"github.com/google/uuid"
)

// Session statuses.
const (
	StatusRecording = "recording"
	StatusAsking    = "asking"
	StatusDrafting  = "drafting"
	StatusReview    = "review"
	StatusSaved     = "saved"
	StatusDiscarded = "discarded"
	StatusFailed    = "failed"
)

// Question is one question that the bot asks after the demo, with the person's answer.
type Question struct {
	Topic  string `json:"topic"` // inputs, success or unclear
	Text   string `json:"text"`
	CardID string `json:"card_id"`
	Answer string `json:"answer,omitempty"`
}

// Input is one value that changes between runs of the taught task.
type Input struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Example     string `json:"example,omitempty"`
}

// Draft is the skill that the bot drafted from the session, as the review card edits it.
type Draft struct {
	Name        string  `json:"name"`
	SkillMD     string  `json:"skill_md"`
	Inputs      []Input `json:"inputs"`
	EvalsJSON   string  `json:"evals_json"`
	BaseSkillID string  `json:"base_skill_id,omitempty"`
	BaseVersion int     `json:"base_version,omitempty"`
	BaseSkillMD string  `json:"base_skill_md,omitempty"`
}

// Session is one teach session of a bot.
type Session struct {
	drel.Model[uuid.UUID] `db:"table=teach_sessions,index=idx_teach_sessions_bot[bot_id,created_at]"`
	SpaceID               uuid.UUID  `db:"space_id,references=spaces,on_delete=cascade"`
	BotID                 uuid.UUID  `db:"bot_id,references=bots,on_delete=cascade"`
	UserID                uuid.UUID  `db:"user_id"`
	ChatID                uuid.UUID  `db:"chat_id"`
	Title                 string     `db:"title"`
	Status                string     `db:"status,check=status IN ('recording','asking','drafting','review','saved','discarded','failed')"`
	TakeoverCard          string     `db:"takeover_card"`
	Pulled                int        `db:"pulled"`
	Questions             []Question `db:"questions"`
	Draft                 Draft      `db:"draft"`
	CardID                string     `db:"card_id"`
	Error                 string     `db:"error"`
	SkillID               *uuid.UUID `db:"skill_id"`
	SkillVersion          int        `db:"skill_version"`
}

// Step is one recorded step of a session: a browser step, a command step, or a note on
// its own. Secret is the kind of a secret field or input; such a step has no value and
// is a login step.
type Step struct {
	drel.Model[uuid.UUID] `db:"table=teach_steps,unique_index=uq_teach_steps[session_id,seq]"`
	SessionID             uuid.UUID `db:"session_id,references=teach_sessions,on_delete=cascade"`
	Seq                   int       `db:"seq"`
	Kind                  string    `db:"kind,check=kind IN ('browser','command','note')"`
	URL                   string    `db:"url"`
	Action                string    `db:"action"`
	Role                  string    `db:"role"`
	Name                  string    `db:"name"`
	Value                 string    `db:"value"`
	Secret                string    `db:"secret"`
	Command               string    `db:"command"`
	Cwd                   string    `db:"cwd"`
	ExitCode              int       `db:"exit_code"`
	Output                string    `db:"output"`
	Notes                 []string  `db:"notes"`
	Screenshot            string    `db:"screenshot" doc:"Object key of the JPEG, or empty."`
	VaultItem             string    `db:"vault_item"`
	Removed               bool      `db:"removed"`
}
