// Package dotenv loads KEY=VALUE pairs from a .env file into the process
// environment without overriding variables that are already set.
package dotenv

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Load reads the file at path and sets each KEY=VALUE pair via os.Setenv.
// A missing file is not an error.
func Load(path string) error {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("invalid .env line: %q", line)
		}

		key = strings.TrimSpace(key)
		if key == "" {
			return fmt.Errorf("invalid .env line with empty key: %q", line)
		}

		value = strings.TrimSpace(value)
		value = strings.Trim(value, "\"'")

		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("set %s from .env: %w", key, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}

	return nil
}
