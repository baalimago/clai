package skills

import (
	"context"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

const (
	configFileName = "skills.json"
	trustFileName  = "skills_trust.json"
)

type Config struct {
	Enabled            bool     `json:"enabled"`
	GlobalSkillDirs    []string `json:"globalSkillDirs"`
	ProjectSkillDirs   []string `json:"projectSkillDirs"`
	TrustAllSkills     bool     `json:"trust_all_skills"`
	MaxActivatedSkills int      `json:"maxActivatedSkills"`
}

var defaultConfig = Config{
	Enabled:            false,
	GlobalSkillDirs:    []string{},
	ProjectSkillDirs:   []string{"./agents/skills", ".claude/skills"},
	TrustAllSkills:     false,
	MaxActivatedSkills: 10,
}

type Options struct {
	ConfigDir      string
	CacheDir       string
	WorkingDir     string
	ConfigOverride *Config
	TrustPrompter  func(context.Context, TrustPrompt) (bool, error)
	LogLevel       LogLevel
	LogQueryText   bool
	KnownToolNames []string
}

type TrustPrompt struct {
	Name        string
	SourceClass string
	Path        string
	Hash        string
	Description string
}

type Metadata struct {
	Name                   string
	Description            string
	WhenToUse              string
	ArgumentHint           string
	Arguments              []string
	DisableModelInvocation bool
	UserInvocable          bool
	AllowedTools           []string
	DisallowedTools        []string
	Model                  string
	Effort                 string
	Context                string
	Agent                  string
	Paths                  []string
	Shell                  string
	Unknown                map[string]string
}

type Diagnostic struct {
	Level   string
	Field   string
	Line    int
	Message string
}

type ParsedSkill struct {
	RawContent     string
	RawBody        string
	NormalizedBody string
	Metadata       Metadata
	Diagnostics    []Diagnostic
}

type Skill struct {
	Name        string
	DisplayName string
	SourceClass string
	SourceRoot  string
	Dir         string
	Path        string
	Parsed      ParsedSkill
	Hash        string
}

type Candidate struct {
	Skill Skill
}

type InvalidSkill struct {
	Class       string
	Root        string
	Dir         string
	Path        string
	Diagnostics []Diagnostic
	Err         error
}

type ShadowedSkill struct {
	Winner Skill
	Loser  Skill
}

type SourceSummary struct {
	Class   string
	Path    string
	Loaded  int
	Invalid int
}

type Summary struct {
	Loaded         int
	Shadowed       int
	Invalid        int
	Sources        []SourceSummary
	Invalids       []InvalidSkill
	ShadowedSkills []ShadowedSkill
}

type ActivationRequest struct {
	Name    string
	RawArgs string
	Args    []string
}

type ActivationRecord struct {
	SkillName string
	RawArgs   string
	Args      []string
}

type ActivationState struct {
	Records      []ActivationRecord
	Allowed      map[string]struct{}
	Disallowed   map[string]struct{}
	LoadedSkills []Skill
}

type LoadedSkill struct {
	Skill         Skill
	RenderedBody  string
	Warnings      []string
	ActiveTools   map[string]pub_models.LLMTool
	ActivationErr string
	RawArgs       string
}

type TrustRecord struct {
	Path        string    `json:"path"`
	Hash        string    `json:"hash"`
	TrustedAt   time.Time `json:"trustedAt"`
	SourceClass string    `json:"sourceClass,omitempty"`
}

type trustCache struct {
	Entries map[string]TrustRecord `json:"entries"`
}
