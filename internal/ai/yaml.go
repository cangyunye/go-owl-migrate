package ai

import (
	"fmt"
	"regexp"
	"strings"
)

// topYAMLKeys are the legal top-level keys of a migrate.yaml; used to locate
// the YAML body inside a chatty model reply.
var topYAMLKeys = regexp.MustCompile(`(?m)^(general|metadata|source|target|ddl|select_gen|export|import|online|agent|owljdbc|ai)\s*:`)

// ExtractYAML pulls the YAML document out of a model reply: fenced ```yaml
// blocks are preferred; otherwise the reply must start with (or contain) a
// known top-level key.
func ExtractYAML(text string) (string, error) {
	text = strings.TrimSpace(text)
	if m := regexp.MustCompile("(?s)```ya?ml\\s*\n(.*?)\n?```").FindStringSubmatch(text); m != nil {
		return strings.TrimSpace(m[1]), nil
	}
	// 无围栏：从第一个已知顶层 key 起截取到结尾（丢弃前后解释文字）。
	loc := topYAMLKeys.FindStringIndex(text)
	if loc == nil {
		return "", fmt.Errorf("未找到 migrate.yaml 顶层键（metadata/source/...）: %.200s", text)
	}
	body := strings.TrimSpace(text[loc[0]:])
	if strings.Contains(body, "```") { // 尾部混入的其他围栏一律丢弃
		body = body[:strings.Index(body, "```")]
		body = strings.TrimSpace(body)
	}
	return body, nil
}

// InjectCredentials substitutes credential placeholders with the real values
// supplied out-of-band by the caller. Placeholders without a value stay
// verbatim so remainingPlaceholders can report them.
func InjectCredentials(yamlText string, creds map[string]string) string {
	for placeholder, value := range creds {
		if value != "" {
			yamlText = strings.ReplaceAll(yamlText, placeholder, value)
		}
	}
	return yamlText
}
