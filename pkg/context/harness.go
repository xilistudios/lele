package context

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const maxHarnessDirEntries = 100
const maxHarnessSkills = 50
const maxAgentsFileBytes = 16384

func truncateUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	idx := max
	for idx > 0 && (s[idx]&0xC0) == 0x80 {
		idx--
	}
	return s[:idx]
}

func BuildHarnessContext() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get current working directory: %w", err)
	}

	entries, err := os.ReadDir(cwd)
	if err != nil {
		return "", fmt.Errorf("failed to read current working directory: %w", err)
	}

	var sb strings.Builder
	sb.WriteString("## Harness Module\n")
	sb.WriteString(fmt.Sprintf("Current Directory: `%s`\n\n", cwd))

	sb.WriteString("### Directory Listing (First-Level)\n")
	if len(entries) == 0 {
		sb.WriteString("No files or directories found.\n")
	} else {
		limit := len(entries)
		if limit > maxHarnessDirEntries {
			limit = maxHarnessDirEntries
		}
		for i := 0; i < limit; i++ {
			name := entries[i].Name()
			if entries[i].IsDir() {
				sb.WriteString(fmt.Sprintf("- %s/\n", name))
			} else {
				sb.WriteString(fmt.Sprintf("- %s\n", name))
			}
		}
		if len(entries) > maxHarnessDirEntries {
			sb.WriteString(fmt.Sprintf("- ... and %d more\n", len(entries)-maxHarnessDirEntries))
		}
	}
	sb.WriteString("\n")

	var allSkills []harnessSkill
	allSkills = append(allSkills, scanSkillsDir(filepath.Join(cwd, ".agents", "skills"), ".agents/skills")...)
	allSkills = append(allSkills, scanSkillsDir(filepath.Join(cwd, ".lele", "skills"), ".lele/skills")...)
	if homeDir, err := os.UserHomeDir(); err == nil {
		allSkills = append(allSkills, scanSkillsDir(filepath.Join(homeDir, ".agents", "skill", "plan"), "~/.agents/skill/plan")...)
	}

	seenNames := make(map[string]struct{}, len(allSkills))
	deduped := allSkills[:0]
	for _, skill := range allSkills {
		if _, seen := seenNames[skill.Name]; seen {
			continue
		}
		seenNames[skill.Name] = struct{}{}
		deduped = append(deduped, skill)
	}
	allSkills = deduped

	truncatedSkills := false
	if len(allSkills) > maxHarnessSkills {
		allSkills = allSkills[:maxHarnessSkills]
		truncatedSkills = true
	}

	if len(allSkills) > 0 {
		sb.WriteString("### Available Skills\n")
		for _, skill := range allSkills {
			desc := skill.Description
			if desc == "" {
				desc = "No description provided."
			}
			sb.WriteString(fmt.Sprintf("- **%s** (source: `%s`): %s\n", skill.Name, skill.Source, desc))
		}
		if truncatedSkills {
			sb.WriteString(fmt.Sprintf("- ... and more (limited to %d skills)\n", maxHarnessSkills))
		}
		sb.WriteString("\n")
	}

	agentsPath := filepath.Join(cwd, "AGENTS.md")
	if data, err := os.ReadFile(agentsPath); err == nil {
		sb.WriteString("### AGENTS.md\n\n")
		truncated := truncateUTF8(string(data), maxAgentsFileBytes)
		sb.WriteString(truncated)
		if len(data) > maxAgentsFileBytes {
			sb.WriteString("\n\n... [AGENTS.md truncated]\n")
		} else {
			sb.WriteString("\n")
		}
	} else {
		agentsPath = filepath.Join(cwd, "agents.md")
		if data, err := os.ReadFile(agentsPath); err == nil {
			sb.WriteString("### AGENTS.md\n\n")
			truncated := truncateUTF8(string(data), maxAgentsFileBytes)
			sb.WriteString(truncated)
			if len(data) > maxAgentsFileBytes {
				sb.WriteString("\n\n... [AGENTS.md truncated]\n")
			} else {
				sb.WriteString("\n")
			}
		}
	}

	return sb.String(), nil
}

type harnessSkill struct {
	Name        string
	Description string
	Source      string
}

func scanSkillsDir(dirPath string, sourceName string) []harnessSkill {
	var list []harnessSkill
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		skillDir := filepath.Join(dirPath, entry.Name())
		skillFilePath := ""
		for _, candidate := range []string{"SKILL.md", "skill.md"} {
			p := filepath.Join(skillDir, candidate)
			if _, err := os.Stat(p); err == nil {
				skillFilePath = p
				break
			}
		}

		if skillFilePath == "" {
			continue
		}

		skill := harnessSkill{
			Name:   entry.Name(),
			Source: sourceName,
		}

		if data, err := os.ReadFile(skillFilePath); err == nil {
			meta := parseSkillMetadata(string(data))
			if meta.Name != "" {
				skill.Name = meta.Name
			}
			skill.Description = meta.Description
		}

		list = append(list, skill)
	}

	return list
}

type skillMetadata struct {
	Name        string
	Description string
}

func parseSkillMetadata(content string) skillMetadata {

	if !strings.HasPrefix(content, "---") {
		return skillMetadata{}
	}
	if len(content) > 3 {
		if content[3] != '\n' && content[3] != '\r' {
			return skillMetadata{}
		}
	}

	var endIdx int
	found := false
	searchFrom := 3
	for {
		pos := strings.Index(content[searchFrom:], "\n---")
		if pos < 0 {
			break
		}
		absPos := searchFrom + pos + 1
		afterDash := absPos + 3
		if afterDash >= len(content) {
			endIdx = absPos
			found = true
			break
		}
		switch content[afterDash] {
		case '\n', '\r':
			endIdx = absPos
			found = true
		case ' ', '\t':
			i := afterDash
			for i < len(content) && (content[i] == ' ' || content[i] == '\t') {
				i++
			}
			if i >= len(content) || content[i] == '\n' || content[i] == '\r' {
				endIdx = absPos
				found = true
			}
		}
		if found {
			break
		}
		searchFrom = afterDash
	}
	if !found {
		return skillMetadata{}
	}

	frontmatter := content[3:endIdx]
	normalized := strings.ReplaceAll(frontmatter, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")

	meta := skillMetadata{}
	lines := strings.Split(normalized, "\n")
	if len(lines) > 0 && lines[0] == "" {
		lines = lines[1:]
	}

	i := 0
	for i < len(lines) {
		line := lines[i]
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			i++
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			i++
			continue
		}

		key := strings.TrimSpace(parts[0])
		rawValue := strings.TrimLeft(parts[1], " \t")

		if rawValue == ">" || rawValue == "|" || rawValue == ">-" || rawValue == "|-" ||
			rawValue == ">+" || rawValue == "|+" {
			blockType := rawValue[0]
			stripFinalNL := true
			if len(rawValue) > 1 {
				switch rawValue[len(rawValue)-1] {
				case '-':
					stripFinalNL = true
				case '+':
					stripFinalNL = false
				}
			}
			i++
			continuationLines := collectIndentedLines(lines, &i)
			joined := strings.Join(continuationLines, "\n")
			if blockType == '>' {
				value := foldBlock(continuationLines)
				if !stripFinalNL {
					value += "\n"
				}
				if key == "name" {
					meta.Name = value
				} else if key == "description" {
					meta.Description = value
				}
			} else {
				value := joined
				if !stripFinalNL {
					value += "\n"
				}
				if key == "name" {
					meta.Name = value
				} else if key == "description" {
					meta.Description = value
				}
			}
			continue
		}

		value := strings.Trim(rawValue, "\"'")
		i++

		var continuations []string
		for i < len(lines) {
			next := lines[i]
			if len(next) == 0 || next[0] != ' ' && next[0] != '\t' {
				break
			}
			trimmed := strings.TrimSpace(next)
			if trimmed == "" {
				break
			}
			continuations = append(continuations, trimmed)
			i++
		}
		if len(continuations) > 0 {
			value = value + " " + strings.Join(continuations, " ")
		}

		if key == "name" {
			meta.Name = value
		} else if key == "description" {
			meta.Description = value
		}
	}

	return meta
}

func collectIndentedLines(lines []string, idx *int) []string {
	var raw []string
	indent := -1
	for *idx < len(lines) {
		line := lines[*idx]
		if len(line) == 0 {
			raw = append(raw, "")
			*idx++
			continue
		}
		if line[0] == ' ' || line[0] == '\t' {
			raw = append(raw, line)
			if indent < 0 {
				indent = countIndent(line)
			}
			*idx++
			continue
		}
		break
	}

	if indent < 0 {
		return nil
	}
	var result []string
	for _, line := range raw {
		if len(line) == 0 {
			result = append(result, "")
		} else {
			stripped := line
			n := 0
			for n < len(stripped) && n < indent && (stripped[n] == ' ' || stripped[n] == '\t') {
				n++
			}
			stripped = stripped[n:]
			result = append(result, strings.TrimRight(stripped, " \t"))
		}
	}

	result = trimBlockEdges(result)
	return result
}

func countIndent(line string) int {
	n := 0
	for n < len(line) && (line[n] == ' ' || line[n] == '\t') {
		n++
	}
	return n
}

func trimBlockEdges(lines []string) []string {
	start := 0
	for start < len(lines) && lines[start] == "" {
		start++
	}
	end := len(lines)
	for end > start && lines[end-1] == "" {
		end--
	}
	return lines[start:end]
}

func foldBlock(lines []string) string {
	var buf strings.Builder
	for i, line := range lines {
		if line == "" {
			buf.WriteByte('\n')
			continue
		}
		if i > 0 && lines[i-1] != "" {
			buf.WriteByte(' ')
		}
		buf.WriteString(line)
	}
	return buf.String()
}
