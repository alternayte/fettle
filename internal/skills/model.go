package skills

import (
	"github.com/alternayte/drel"
	"github.com/google/uuid"
)

// Skill is a SKILL.md folder in a space's library (§13.1).
type Skill struct {
	drel.Model[uuid.UUID] `db:"unique_index=uq_skills_space_name[space_id,name]"`
	SpaceID               uuid.UUID `db:"space_id,references=spaces,on_delete=cascade"`
	Name                  string    `db:"name"`
	Description           string    `db:"description"`
	Source                string    `db:"source,check=source IN ('local','skael')"`
	CurrentVersion        int       `db:"current_version"`
}

// SkillVersion is one stored version of a skill. The archive is in the object store,
// sealed with the space data key.
type SkillVersion struct {
	drel.Model[uuid.UUID] `db:"unique_index=uq_skill_versions[skill_id,version]"`
	SkillID               uuid.UUID `db:"skill_id,references=skills,on_delete=cascade"`
	Version               int       `db:"version"`
	SHA256                string    `db:"sha256"`
	ObjectKey             string    `db:"object_key"`
	SkillMD               string    `db:"skill_md"`
	Files                 []string  `db:"files"`
	HasEvals              bool      `db:"has_evals"`
	// Upstream is the Skael version this version came from (0 for local skills).
	Upstream  int    `db:"upstream"`
	CreatedBy string `db:"created_by"`
}

// BotSkill gives a bot a skill: its name and description go into the bot's skill index.
type BotSkill struct {
	drel.Model[uuid.UUID] `db:"unique_index=uq_bot_skills[bot_id,skill_id]"`
	BotID                 uuid.UUID `db:"bot_id,references=bots,on_delete=cascade"`
	SkillID               uuid.UUID `db:"skill_id,references=skills,on_delete=cascade"`
}
