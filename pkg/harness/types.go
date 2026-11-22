package harness

type Source string

const (
	SourceConfig    Source = "config"
	SourceGlobal    Source = "global"
	SourceWorkspace Source = "workspace"
	SourceDirectory Source = "directory"
)

type CommandDef struct {
	Description        string `json:"description,omitempty"`
	Agent              string `json:"agent,omitempty"`
	Model              string `json:"model,omitempty"`
	Template           string `json:"template"`
	AllowShell         bool   `json:"allow_shell,omitempty"`
	AllowAbsoluteFiles *bool  `json:"allow_absolute_files,omitempty"`
}

type Command struct {
	Name               string
	Description        string
	Template           string
	Agent              string
	Model              string
	AllowShell         bool
	AllowAbsoluteFiles *bool
	Source             Source
	Path               string
}

func (d CommandDef) ToCommand(name string, source Source, path string) *Command {
	return &Command{
		Name:               name,
		Description:        d.Description,
		Template:           d.Template,
		Agent:              d.Agent,
		Model:              d.Model,
		AllowShell:         d.AllowShell,
		AllowAbsoluteFiles: d.AllowAbsoluteFiles,
		Source:             source,
		Path:               path,
	}
}
