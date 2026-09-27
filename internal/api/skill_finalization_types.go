package api

import "github.com/Agent-Remote/agent-remote-node/internal/skillmanager"

// SkillFinalizationInput identifies one immutable Helper-retained stopped-session capture.
type SkillFinalizationInput = skillmanager.FinalizationInput

// SkillFinalization distinguishes complete input persistence from account publication.
type SkillFinalization = skillmanager.FinalizationReceipt

// SkillPublication records the whole-directory publication decision, including retained conflicts.
type SkillPublication = skillmanager.PublicationReceipt
