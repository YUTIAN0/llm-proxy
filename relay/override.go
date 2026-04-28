package relay

import (
	"github.com/llm-proxy/config"
)

// ApplyParamOverride applies parameter override rules to a request body map.
func ApplyParamOverride(body map[string]any, rules []config.ParamOverrideRule) map[string]any {
	if len(rules) == 0 {
		return body
	}

	for _, rule := range rules {
		switch rule.Mode {
		case "set":
			body[rule.Path] = rule.Value
		case "delete":
			delete(body, rule.Path)
		case "prepend":
			if current, ok := body[rule.Path]; ok {
				if s, ok := current.(string); ok {
					body[rule.Path] = rule.Value.(string) + s
				}
			} else {
				body[rule.Path] = rule.Value
			}
		case "append":
			if current, ok := body[rule.Path]; ok {
				if s, ok := current.(string); ok {
					body[rule.Path] = s + rule.Value.(string)
				}
			} else {
				body[rule.Path] = rule.Value
			}
		}
	}

	return body
}
