package skillsdirectory

import (
	"context"
	"fmt"
	"io"
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
	output, err := runCommand(ctx, "npx", "-y", "skills", "search", query)
	if err != nil {
		return nil, fmt.Errorf("skills search failed: %w", err)
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
	output, err := runCommand(ctx, "npx", "-y", "skills", "use", installRef)
	if err != nil {
		return nil, fmt.Errorf("skills use failed: %w", err)
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
	output, err := runCommand(ctx, "npx", "-y", "skills", "add", source, "--list", "--yes")
	if err != nil {
		return nil, fmt.Errorf("skills list failed: %w", err)
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
	_, err := runCommand(ctx, "npx", args...)
	if err != nil {
		return fmt.Errorf("skills install failed: %w", err)
	}
	return nil
}

// DefaultTimeout is the default timeout for skills.sh operations.
const DefaultTimeout = 60 * time.Second

// maxOutputSize limits subprocess output to 10MB — prevents OOM from
// malicious or buggy npx skills commands producing unbounded output.
const maxOutputSize = 10 << 20

// runCommand executes an npx command and returns its combined output,
// limited to maxOutputSize bytes to prevent memory exhaustion.
func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(cmd.Environ(), "CI=true") // suppress interactive prompts

	// Get a pipe to stdout+stderr so we can limit the output size
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw

	if err := cmd.Start(); err != nil {
		pw.Close()
		return nil, err
	}

	// Read output in a goroutine with size limit
	type readResult struct {
		data []byte
		err  error
	}
	ch := make(chan readResult, 1)
	go func() {
		data, err := io.ReadAll(io.LimitReader(pr, maxOutputSize))
		ch <- readResult{data, err}
	}()

	// Wait for command to finish, then close the pipe and read output
	waitErr := cmd.Wait()
	pw.Close()
	res := <-ch

	if waitErr != nil {
		return res.data, fmt.Errorf("%s failed: %w (output: %s)", name, waitErr, string(res.data))
	}
	if res.err != nil {
		return nil, fmt.Errorf("%s output read failed: %w", name, res.err)
	}
	return res.data, nil
}
