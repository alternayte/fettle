package bots

import (
	"time"

	"github.com/alternayte/drel"
	"github.com/google/uuid"
)

// Bot is a persistent teammate.
type Bot struct {
	drel.Model[uuid.UUID] `db:"index=idx_bots_space[space_id,status]"`
	SpaceID               uuid.UUID  `db:"space_id,references=spaces,on_delete=cascade"`
	Name                  string     `db:"name"`
	Job                   string     `db:"job"`
	Instructions          string     `db:"instructions"`
	InstructionsRev       int        `db:"instructions_rev"`
	ZoneID                *uuid.UUID `db:"zone_id"`
	Avatar                string     `db:"avatar"`
	IsPrimary             bool       `db:"is_primary"`
	PublicDesc            string     `db:"public_desc"`
	Status                string     `db:"status,check=status IN ('active','archived')"`
	CreatedByKind         string     `db:"created_by_kind,check=created_by_kind IN ('user','bot','system')"`
	CreatedBy             string     `db:"created_by"`
}

// InstructionVersion is one saved version of a bot's instructions.
type InstructionVersion struct {
	drel.Model[uuid.UUID] `db:"table=bot_instructions,unique_index=uq_bot_instructions[bot_id,rev]"`
	BotID                 uuid.UUID `db:"bot_id,references=bots,on_delete=cascade"`
	Rev                   int       `db:"rev"`
	Instructions          string    `db:"instructions"`
	AuthorKind            string    `db:"author_kind"`
	AuthorID              string    `db:"author_id"`
}

// Memory is the MEMORY.md content of a bot, or the shared memory of a space when BotID is nil.
type Memory struct {
	drel.Model[uuid.UUID] `db:"table=bot_memory,unique_index=uq_bot_memory[space_id,bot_key]"`
	SpaceID               uuid.UUID  `db:"space_id,references=spaces,on_delete=cascade"`
	BotID                 *uuid.UUID `db:"bot_id,references=bots,on_delete=cascade"`
	BotKey                string     `db:"bot_key"`
	Content               string     `db:"content"`
	Rev                   int        `db:"rev"`
	UpdatedByKind         string     `db:"updated_by_kind"`
	UpdatedBy             string     `db:"updated_by"`
	Origin                string     `db:"origin"`
}

// OpenWork is one started and unfinished item that a bot keeps.
type OpenWork struct {
	drel.Model[uuid.UUID] `db:"table=open_work,index=idx_open_work_due[due_at]"`
	BotID                 uuid.UUID  `db:"bot_id,references=bots,on_delete=cascade"`
	Text                  string     `db:"text"`
	DueAt                 *time.Time `db:"due_at"`
	DoneAt                *time.Time `db:"done_at"`
}
