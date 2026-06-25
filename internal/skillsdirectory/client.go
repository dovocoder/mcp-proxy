package skillsdirectory

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// SearchResult represents a single skill from skills.sh search.
type SearchResult struct {
	Source     string // e.g. "vercel-labs/agent-skills"
	Slug       string // e.g. "vercel-react-best-practices"
	Installs   string // e.g. "502.9K installs"
	URL        string // e.g. "https://skills.sh/vercel-labs/agent-skills/vercel-react-best-practices"
	InstallRef string // e.g. "vercel-labs/agent-skills@vercel-react-best-practices"
}

// Search searches the skills.sh directory for skills matching the query.
// Uses `npx skills search <query>` which works without Vercel OIDC auth.
func Search(ctx context.Context, query string) ([]SearchResult, error) {
	cmd := exec.CommandContext(ctx, "npx", "-y", "skills", "search", query)
	cmd.Env = append(cmd.Environ(), "CI=true") // suppress interactive prompts
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("skills search failed: %w (output: %s)", err, string(output))
	}
	return parseSearchOutput(string(output)), nil
}

// searchResultRegex matches lines like:
// vercel-labs/agent-skills@vercel-react-best-practices 502.9K installs
var searchResultRegex = regexp.MustCompile(`^(.+?)@(.+?)\s+(.+?install.+?)$`)

// urlLineRegex matches the URL line that follows each result
var urlLineRegex = regexp.MustCompile(`└\s+(https://skills\.sh/\S+)`)

func parseSearchOutput(output string) []SearchResult {
	var results []SearchResult
	lines := strings.Split(output, "\n")

	for i, line := range lines {
		line = strings.TrimSpace(line)
		matches := searchResultRegex.FindStringSubmatch(line)
		if matches == nil {
			continue
		}

		result := SearchResult{
			Source:     matches[1],
			Slug:       matches[2],
			Installs:   matches[3],
			InstallRef: matches[1] + "@" + matches[2],
		}

		// Look for URL on the next line
		if i+1 < len(lines) {
			urlMatches := urlLineRegex.FindStringSubmatch(strings.TrimSpace(lines[i+1]))
			if urlMatches != nil {
				result.URL = urlMatches[1]
			}
		}

		results = append(results, result)
	}

	return results
}

// SkillFile represents a file in a skill from skills.sh.
type SkillFile struct {
	Path     string
	Contents string
}

// SkillDetail contains the full content of a skill from skills.sh.
type SkillDetail struct {
	Source   string
	Slug     string
	Content  string // the SKILL.md content
	Files    []SkillFile
}

// GetSkillContent fetches the full content of a skill from skills.sh.
// Uses `npx skills use <source>@<slug>` which outputs the SKILL.md as text.
func GetSkillContent(ctx context.Context, installRef string) (*SkillDetail, error) {
	cmd := exec.CommandContext(ctx, "npx", "-y", "skills", "use", installRef)
	cmd.Env = append(cmd.Environ(), "CI=true")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("skills use failed: %w (output: %s)", err, string(output))
	}

	// Parse the output — it contains the SKILL.md content
	// The output format is:
	// "You are being given a Skill to execute...\n\n<SKILL.md>\n{content}\n</SKILL.md>"
	content := string(output)

	// Extract the SKILL.md content between <SKILL.md> and </SKILL.md>
	startMarker := "<SKILL.md>"
	endMarker := "</SKILL.md>"
	startIdx := strings.Index(content, startMarker)
	endIdx := strings.Index(content, endMarker)

	var skillContent string
	if startIdx >= 0 && endIdx > startIdx {
		skillContent = content[startIdx+len(startMarker) : endIdx]
		skillContent = strings.TrimSpace(skillContent)
	} else {
		// If markers not found, use the raw output (minus the preamble)
		skillContent = content
	}

	// Parse source and slug from installRef (format: owner/repo@slug)
	parts := strings.SplitN(installRef, "@", 2)
	source := ""
	slug := installRef
	if len(parts) == 2 {
		source = parts[0]
		slug = parts[1]
	}

	return &SkillDetail{
		Source:  source,
		Slug:    slug,
		Content: skillContent,
	}, nil
}

// ListSkills lists available skills in a GitHub repo (owner/repo format).
// Uses `npx skills add <source> --list` which fetches skill metadata from GitHub.
func ListSkills(ctx context.Context, source string) ([]SkillInfo, error) {
	cmd := exec.CommandContext(ctx, "npx", "-y", "skills", "add", source, "--list", "--yes")
	cmd.Env = append(cmd.Environ(), "CI=true")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("skills list failed: %w (output: %s)", err, string(output))
	}
	return parseListOutput(string(output), source), nil
}

// SkillInfo represents a skill found in a GitHub repo.
type SkillInfo struct {
	Source      string // e.g. "vercel-labs/agent-skills"
	Slug        string // e.g. "vercel-react-best-practices"
	Name        string // human-readable name
	Description string // skill description
	InstallRef  string // e.g. "vercel-labs/agent-skills@vercel-react-best-practices"
}

func parseListOutput(output string, source string) []SkillInfo {
	var skills []SkillInfo
	lines := strings.Split(output, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "│") || strings.HasPrefix(line, "┌") ||
			strings.HasPrefix(line, "◒") || strings.HasPrefix(line, "◇") ||
			strings.HasPrefix(line, "◐") || strings.HasPrefix(line, "◓") ||
			strings.HasPrefix(line, "◑") || strings.HasPrefix(line, "█") ||
			strings.HasPrefix(line, "╚") || strings.HasPrefix(line, "╗") ||
			strings.HasPrefix(line, "╔") || strings.HasPrefix(line, "│") {
			// Skip UI decoration lines — but check for skill names
			// Skill names appear as "│    skill-name" (indented)
			if strings.HasPrefix(line, "│") {
				content := strings.TrimSpace(strings.TrimPrefix(line, "│"))
				if content != "" && !strings.Contains(content, "Tip:") &&
					!strings.Contains(content, "Source:") &&
					!strings.Contains(content, "Available") &&
					!strings.Contains(content, "Found") &&
					!strings.Contains(content, "Fetching") {
					// This might be a skill slug or description
					// Skill slugs are single words, descriptions are longer
					if !strings.Contains(content, " ") || len(content) < 60 {
						skills = append(skills, SkillInfo{
							Source:     source,
							Slug:       content,
							Name:       content,
							InstallRef: source + "@" + content,
						})
					}
				}
			}
			continue
		}
	}

	return skills
}

// InstallSkill installs a skill from skills.sh to the local filesystem.
// Uses `npx skills add <source>@<slug>` with the specified agent.
func InstallSkill(ctx context.Context, installRef string, agent string) error {
	args := []string{"-y", "skills", "add", installRef, "--yes"}
	if agent != "" {
		args = append(args, "-a", agent)
	}
	cmd := exec.CommandContext(ctx, "npx", args...)
	cmd.Env = append(cmd.Environ(), "CI=true")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("skills install failed: %w (output: %s)", err, string(output))
	}
	return nil
}

// DefaultTimeout is the default timeout for skills.sh operations.
const DefaultTimeout = 60 * time.Second
