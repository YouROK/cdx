package agent

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/yourok/cdx/internal/config"
	"github.com/yourok/cdx/internal/registry"
)

// ContextFile is a loaded context file.
type ContextFile struct {
	Dir     string
	Name    string
	Content string
}

// PromptParts holds the system context blocks in spec order.
type PromptParts struct {
	// Stable is the unchanging part, sent first so provider caching works.
	Stable string
	// Files holds agent.md and skills.md in the required order.
	Files []ContextFile
	// Environment describes the runtime.
	Environment string
}

// Render assembles the whole system message.
func (p PromptParts) Render() string {
	var sb strings.Builder
	sb.WriteString(strings.TrimSpace(p.Stable))

	for _, f := range p.Files {
		sb.WriteString("\n\n## ")
		sb.WriteString(f.Name)
		sb.WriteString(" (")
		sb.WriteString(f.Dir)
		sb.WriteString(")\n\n")
		sb.WriteString(f.Content)
	}

	if p.Environment != "" {
		sb.WriteString("\n\n")
		sb.WriteString(p.Environment)
	}
	return sb.String()
}

// Hash returns the stable part for comparison with the history.
func (p PromptParts) Hash() string {
	return HashPrompt(p.Stable, p.Files)
}

// BuildParts assembles the system context blocks.
//
// The order is prompt, context files, environment. The tool list is not
// part of the prompt: it travels as a separate request parameter.
func BuildParts(tmpl string, cfg *config.Config, cwd, sessionRoot string, files []ContextFile) PromptParts {
	stable := strings.NewReplacer(
		"{{max_iterations}}", fmt.Sprintf("%d", cfg.Agent.MaxIterations),
	).Replace(tmpl)

	return PromptParts{
		Stable:      stable,
		Files:       files,
		Environment: buildEnvironment(cfg, cwd, sessionRoot, nil),
	}
}

// buildEnvironment describes the invocation environment.
func buildEnvironment(cfg *config.Config, cwd, sessionRoot string, specs []map[string]any) string {
	var sb strings.Builder
	sb.WriteString("## Environment\n")
	sb.WriteString("- os: " + runtime.GOOS + "\n")
	sb.WriteString("- arch: " + runtime.GOARCH + "\n")
	sb.WriteString("- shell: " + shellName() + "\n")
	sb.WriteString("- cwd: " + cwd + "\n")
	if sessionRoot != "" {
		sb.WriteString("- session root: " + sessionRoot + "\n")
	}
	sb.WriteString("- date: " + time.Now().Format("2006-01-02") + "\n")

	if branch, ok := gitBranch(cwd); ok {
		sb.WriteString("- git branch: " + branch + "\n")
	}
	if cfg != nil {
		sb.WriteString("- permissions: read=" + cfg.Perms.Read +
			", edit=" + cfg.Perms.Edit +
			", dangerous=" + cfg.Perms.Dangerous + "\n")
	}

	// With no tools the model must not promise file work: the block says
	// plainly that nothing is available.
	if len(specs) == 0 {
		sb.WriteString("\n## Tools\n")
		sb.WriteString("No tools are available in this invocation. You cannot read, " +
			"write or search files, run commands or make requests. " +
			"Answer from the request alone and say plainly what you cannot do.\n")
		return strings.TrimRight(sb.String(), "\n")
	}

	// The heading is printed only when there is something to list.
	var list strings.Builder
	for _, spec := range specs {
		fn, ok := spec["function"].(map[string]any)
		if !ok {
			continue
		}
		name, _ := fn["name"].(string)
		if name == "" {
			continue
		}
		desc, _ := fn["description"].(string)
		list.WriteString("- " + name)
		if desc != "" {
			list.WriteString(": " + desc)
		}
		list.WriteString("\n")
	}
	if list.Len() > 0 {
		sb.WriteString("\n## Tools\n")
		sb.WriteString(list.String())
	}
	return strings.TrimRight(sb.String(), "\n")
}

// BuildPrompt assembles the system message as one string.
func BuildPrompt(tmpl string, cfg *config.Config, cwd, sessionRoot string) string {
	return BuildParts(tmpl, cfg, cwd, sessionRoot, nil).Render()
}

// WithTools adds the tool list to the environment block.
func (p PromptParts) WithTools(specs []map[string]any, cfg *config.Config, cwd, sessionRoot string) PromptParts {
	p.Environment = buildEnvironment(cfg, cwd, sessionRoot, specs)
	return p
}

func osName() string { return runtime.GOOS }

func archName() string { return runtime.GOARCH }

// shellName returns the user shell.
func shellName() string {
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	return "sh"
}

// PromptTemplate returns the active prompt.
func PromptTemplate(custom string) string {
	if strings.TrimSpace(custom) != "" {
		return custom
	}
	return StandardPrompt
}

// ToolNames lists tool names for substitution into the prompt.
func ToolNames(reg *registry.Registry) []string {
	if reg == nil {
		return nil
	}
	return reg.Names()
}
