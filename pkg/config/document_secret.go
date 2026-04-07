package config

import (
	"encoding/json"
	"os"
	"regexp"
)

var envPlaceholderRegex = regexp.MustCompile(`^\{\{ENV_([A-Za-z_][A-Za-z0-9_]*)(?::([^}]*))?\}\}$`)

func parseSecretValue(raw json.RawMessage, path string, secretsByPath map[string]string) SecretValue {
	var strValue string
	if err := json.Unmarshal(raw, &strValue); err == nil {
		// Check whether the string is a placeholder.
		matches := envPlaceholderRegex.FindStringSubmatch(strValue)
		if len(matches) >= 2 {
			var envDefault *string
			if len(matches) >= 3 && matches[2] != "" {
				def := matches[2]
				envDefault = &def
			}
			secretsByPath[path] = "env"
			return SecretValue{
				Mode:       SecretModeEnv,
				EnvName:    matches[1],
				EnvDefault: envDefault,
				HasEnvVar:  os.Getenv(matches[1]) != "",
			}
		}

		if strValue == "" {
			secretsByPath[path] = "empty"
			return SecretValue{Mode: SecretModeEmpty}
		}

		secretsByPath[path] = "literal"
		return SecretValue{
			Mode:  SecretModeLiteral,
			Value: strValue,
		}
	}

	// If it is not a string, it may be null or another type.
	return SecretValue{Mode: SecretModeEmpty}
}
