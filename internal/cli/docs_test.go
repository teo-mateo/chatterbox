package cli

import (
	"os"
	"strings"
	"testing"
)

// The paste-in snippet is the skill without its header; keep them in step.
func TestSnippetMatchesSkill(t *testing.T) {
	skill, err := os.ReadFile("../../skills/chatterbox/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	snippet, err := os.ReadFile("../../docs/agent-snippet.md")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(skill), "---", 3)
	if len(parts) != 3 || !strings.Contains(parts[1], "name: chatterbox") || !strings.Contains(parts[1], "description:") {
		t.Fatal("SKILL.md must start with a front matter block naming the skill")
	}
	body := strings.TrimLeft(strings.Replace(strings.TrimLeft(parts[2], "\n"), "# chatterbox\n", "", 1), "\n")
	body = strings.ReplaceAll(body, "\n## ", "\n### ")
	_, got, found := strings.Cut(string(snippet), "## Talking to other agents with chatterbox\n\n")
	if !found {
		t.Fatal("docs/agent-snippet.md lacks its heading")
	}
	if got != body {
		t.Error("docs/agent-snippet.md and skills/chatterbox/SKILL.md have drifted apart; update both")
	}
}

// Every command of the usage text is covered by the agent instructions.
func TestSkillCoversEveryCommand(t *testing.T) {
	skill, err := os.ReadFile("../../skills/chatterbox/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"chatterbox listen --as", "chatterbox publish --as", "chatterbox send --as", "chatterbox whos-there --as",
		"chatterbox channels --as", "chatterbox create-channel --as", "--important", "--private", "--wake addressed",
		"replayed", "(2/3)", "Never acknowledge an acknowledgement", "stop the background listener", "Neither is verified",
	} {
		if !strings.Contains(string(skill), want) {
			t.Errorf("the skill does not mention %q", want)
		}
	}
}
