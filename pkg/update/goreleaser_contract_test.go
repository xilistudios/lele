package update

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

const goreleaserConfigPath = "../../.goreleaser.yaml"

var goreleaserTemplateAction = regexp.MustCompile(`\{\{-?\s*(.*?)\s*-?\}\}`)

func TestChecksumsNameMatchesGoreleaserConfig(t *testing.T) {
	const version = "9.9.9"

	nameTemplate := goreleaserBlockValue(t, "checksum", "name_template")
	rendered := renderGoreleaserTemplate(t, nameTemplate, version)

	if got, want := ChecksumsName(version), rendered; got != want {
		t.Errorf(
			"ChecksumsName(%q) = %q but .goreleaser.yaml publishes %q; every client would look for a file the release does not contain",
			version, got, want,
		)
	}
}

func TestChecksumsSignatureNameMatchesGoreleaserConfig(t *testing.T) {
	const version = "9.9.9"

	signatureTemplate := goreleaserBlockValue(t, "signs", "signature")
	rendered := strings.ReplaceAll(signatureTemplate, "${artifact}", ChecksumsName(version))

	if got, want := ChecksumsSignatureName(version), rendered; got != want {
		t.Errorf(
			"ChecksumsSignatureName(%q) = %q but .goreleaser.yaml publishes %q; every client would reject the release as unsigned",
			version, got, want,
		)
	}
}

func TestChecksumsSignatureIsSuffixedExactlyOnce(t *testing.T) {
	signatureTemplate := goreleaserBlockValue(t, "signs", "signature")
	if signatureTemplate != "${artifact}"+ReleaseSignatureSuffix {
		t.Errorf(
			".goreleaser.yaml signature = %q, want %q",
			signatureTemplate, "${artifact}"+ReleaseSignatureSuffix,
		)
	}
}

func goreleaserBlockValue(t *testing.T, block, key string) string {
	t.Helper()

	source, err := os.ReadFile(goreleaserConfigPath)
	if err != nil {
		t.Fatalf("reading %s: %v", goreleaserConfigPath, err)
	}

	lines := strings.Split(string(source), "\n")
	start := -1
	for index, line := range lines {
		if strings.TrimRight(line, " \t") == block+":" {
			start = index + 1
			break
		}
	}
	if start < 0 {
		t.Fatalf("%s has no top-level %q block", goreleaserConfigPath, block)
	}

	for _, line := range lines[start:] {
		if line != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			break
		}
		if value, ok := yamlScalar(line, key); ok {
			return value
		}
	}
	t.Fatalf("%s has no %q key inside the %q block", goreleaserConfigPath, key, block)
	return ""
}

func yamlScalar(line, key string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, key+":") {
		return "", false
	}
	value := strings.TrimSpace(strings.TrimPrefix(trimmed, key+":"))
	if len(value) >= 2 {
		if (value[0] == '\'' && value[len(value)-1] == '\'') ||
			(value[0] == '"' && value[len(value)-1] == '"') {
			value = value[1 : len(value)-1]
		}
	}
	return value, true
}

func renderGoreleaserTemplate(t *testing.T, template, version string) string {
	t.Helper()

	var unrenderable string
	rendered := goreleaserTemplateAction.ReplaceAllStringFunc(template, func(action string) string {
		match := goreleaserTemplateAction.FindStringSubmatch(action)
		if len(match) < 2 || match[1] != ".Version" {
			unrenderable = action
			return action
		}
		return version
	})
	if unrenderable != "" {
		t.Fatalf("name_template %q uses %s; this contract test only renders {{ .Version }}", template, unrenderable)
	}
	if strings.Contains(rendered, "{{") {
		t.Fatalf("name_template %q still contains a template action after rendering: %q", template, rendered)
	}
	return rendered
}
