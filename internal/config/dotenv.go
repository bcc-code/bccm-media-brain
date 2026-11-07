package config

import (
	"errors"
	"fmt"
	"io/fs"

	"github.com/joho/godotenv"
)

// EnvFileVar names the variable that overrides the dotenv file path (default ".env").
const EnvFileVar = "MEDIABRAIN_ENV_FILE"

// dotenvLookup returns a lookup that prefers real environment variables and
// falls back to values from the dotenv file. A missing file is not an error.
func dotenvLookup(env func(string) (string, bool), path string) (func(string) (string, bool), error) {
	vals, err := godotenv.Read(path)
	if errors.Is(err, fs.ErrNotExist) {
		return env, nil
	}
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	return func(k string) (string, bool) {
		if v, ok := env(k); ok {
			return v, true
		}
		v, ok := vals[k]
		return v, ok
	}, nil
}
