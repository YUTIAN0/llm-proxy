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
			if rv, ok := rule.Value.(string); ok {
				if current, ok := body[rule.Path]; ok {
					if s, ok := current.(string); ok {
						body[rule.Path] = rv + s
					}
				} else {
					body[rule.Path] = rv
				}
			}
		case "append":
			if rv, ok := rule.Value.(string); ok {
				if current, ok := body[rule.Path]; ok {
					if s, ok := current.(string); ok {
						body[rule.Path] = s + rv
					}
				} else {
					body[rule.Path] = rv
				}
			}
		}
	}

	return body
}
