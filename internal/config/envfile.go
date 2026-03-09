package config

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

func ReadOptionalEnvFile(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("open env file %s: %w", path, err)
	}
	defer file.Close()

	values, err := ParseEnv(file)
	if err != nil {
		return nil, fmt.Errorf("parse env file %s: %w", path, err)
	}
	return values, nil
}

func ParseEnv(reader io.Reader) (map[string]string, error) {
	values := map[string]string{}
	scanner := bufio.NewScanner(reader)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid env line %d", lineNo)
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if len(value) >= 2 {
			switch {
			case value[0] == '"' && value[len(value)-1] == '"':
				unquoted, err := strconv.Unquote(value)
				if err == nil {
					value = unquoted
				}
			case value[0] == '\'' && value[len(value)-1] == '\'':
				value = value[1 : len(value)-1]
			}
		}
		values[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return values, nil
}

func ParseEnviron(environ []string) map[string]string {
	values := map[string]string{}
	for _, entry := range environ {
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) != 2 {
			continue
		}
		values[parts[0]] = parts[1]
	}
	return values
}

func WriteEnvFile(path string, values map[string]string) error {
	var builder strings.Builder
	for groupIndex, group := range envGroups {
		for _, key := range group {
			builder.WriteString(key)
			builder.WriteString("=")
			builder.WriteString(formatEnvValue(values[key]))
			builder.WriteString("\n")
		}
		if groupIndex < len(envGroups)-1 {
			builder.WriteString("\n")
		}
	}

	return os.WriteFile(path, []byte(builder.String()), 0o600)
}

func formatEnvValue(value string) string {
	if value == "" {
		return ""
	}
	if strings.ContainsAny(value, " \t#\"'\\") || strings.Contains(value, "\n") {
		return strconv.Quote(value)
	}
	return value
}
