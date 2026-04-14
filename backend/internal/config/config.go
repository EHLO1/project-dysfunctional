package config

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"
)

type AppEnvironment string

const (
	AppEnvironmentProduction  AppEnvironment = "production"
	AppEnvironmentDevelopment AppEnvironment = "development"
	AppEnvironmentTest        AppEnvironment = "test"
)

// Config holds all application configuration.
// Fields tagged with `env` will be loaded from the corresponding environment variable.
// Fields with `options:"file"` support Docker secrets via the _FILE suffix.
// Available options: file, toLower, trimTrailingSlash
type Config struct {
	AppUrl         string         `env:"APP_URL" default:"http://localhost:3552"`
	DatabaseURL    string         `env:"DATABASE_URL" default:"file:data/callout.db?_pragma=journal_mode(WAL)&_pragma=busy_timeout(2500)&_txlock=immediate" options:"file"`
	AllowDowngrade bool           `env:"ALLOW_DOWNGRADE" default:"false"`
	Port           string         `env:"PORT" default:"3552"`
	Listen         string         `env:"LISTEN" default:""`
	TLSEnabled     bool           `env:"TLS_ENABLED" default:"false"`
	TLSCertFile    string         `env:"TLS_CERT_FILE" default:""`
	TLSKeyFile     string         `env:"TLS_KEY_FILE" default:""`
	Environment    AppEnvironment `env:"ENVIRONMENT" default:"production"`
}

func Load() *Config {
	cfg := &Config{}
	loadFromEnv(cfg)
	applyOptions(cfg)

	return cfg
}

// loadFromEnv uses reflection to load configuration from environment variables.
func loadFromEnv(cfg *Config) {
	v := reflect.ValueOf(cfg).Elem()
	visitConfigFields(v, func(field reflect.Value, fieldType reflect.StructField) {
		envTag := fieldType.Tag.Get("env")
		if envTag == "" {
			return
		}

		defaultValue := fieldType.Tag.Get("default")

		// Get the environment value directly first
		envValue := trimQuotes(os.Getenv(envTag))
		if envValue == "" {
			envValue = defaultValue
		}

		setFieldValueInternal(field, fieldType, envValue)
	})
}

// applyOptions processes special options for Config fields after initial load.
func applyOptions(cfg *Config) {
	v := reflect.ValueOf(cfg).Elem()
	visitConfigFields(v, func(field reflect.Value, fieldType reflect.StructField) {
		optionsTag := fieldType.Tag.Get("options")
		if optionsTag == "" {
			return
		}

		options := strings.SplitSeq(optionsTag, ",")
		for option := range options {
			switch strings.TrimSpace(option) {
			case "file":
				resolveFileBasedEnvVariable(field, fieldType)
			case "toLower":
				if field.Kind() == reflect.String {
					field.SetString(strings.ToLower(field.String()))
				}
			case "trimTrailingSlash":
				if field.Kind() == reflect.String {
					field.SetString(strings.TrimRight(field.String(), "/"))
				}
			}
		}
	})
}

func visitConfigFields(v reflect.Value, fn func(reflect.Value, reflect.StructField)) {
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return
	}

	t := v.Type()
	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		fieldType := t.Field(i)

		if fieldType.Anonymous {
			if field.Kind() == reflect.Struct {
				visitConfigFields(field, fn)
				continue
			}
			if field.Kind() == reflect.Pointer && field.Type().Elem().Kind() == reflect.Struct {
				if field.IsNil() {
					if field.CanSet() {
						field.Set(reflect.New(field.Type().Elem()))
					} else {
						continue
					}
				}
				visitConfigFields(field.Elem(), fn)
				continue
			}
		}

		fn(field, fieldType)
	}
}

// resolveFileBasedEnvVariable checks if an environment variable with the suffix "_FILE" is set,
// reads the content of the file specified by that variable, and sets the corresponding field's value.
func resolveFileBasedEnvVariable(field reflect.Value, fieldType reflect.StructField) {
	// Only process string and []byte fields
	isString := field.Kind() == reflect.String
	isByteSlice := field.Kind() == reflect.Slice && field.Type().Elem().Kind() == reflect.Uint8
	if !isString && !isByteSlice {
		return
	}

	// Only process fields with the "env" tag
	envTag := fieldType.Tag.Get("env")
	if envTag == "" {
		return
	}

	// Check both double underscore (__FILE) and single underscore (_FILE) variants
	// Double underscore takes precedence
	var filePath string
	for _, suffix := range []string{"__FILE", "_FILE"} {
		if fp := os.Getenv(envTag + suffix); fp != "" {
			filePath = fp
			break
		}
	}

	if filePath == "" {
		return
	}

	fileContent, err := os.ReadFile(filePath) //nolint:gosec // file path intentionally comes from *_FILE env vars for Docker secrets
	if err != nil {
		slog.Warn("Failed to read secret from file, falling back to direct env var",
			"error", err)
		return
	}

	// Log when file value overrides a direct env var
	if os.Getenv(envTag) != "" {
		slog.Debug("Using secret from file, overriding direct env var")
	}

	if isString {
		field.SetString(strings.TrimSpace(string(fileContent)))
	} else {
		field.SetBytes(fileContent)
	}
}

// setFieldValueInternal sets a reflect.Value from a string based on the field's type.
func setFieldValueInternal(field reflect.Value, fieldType reflect.StructField, value string) {
	if !field.CanSet() {
		return
	}

	if field.Kind() == reflect.String {
		field.SetString(value)
		return
	}

	if field.Kind() == reflect.Bool {
		if b, err := strconv.ParseBool(value); err == nil {
			field.SetBool(b)
		}
		return
	}

	if field.Kind() == reflect.Uint32 {
		// Handle os.FileMode (which is uint32)
		if i, err := strconv.ParseUint(value, 8, 32); err == nil {
			field.SetUint(i)
		}
		return
	}

	if field.Kind() == reflect.Int {
		if i, err := strconv.Atoi(value); err == nil {
			field.SetInt(int64(i))
		}
		return
	}

	if field.Type() == reflect.TypeFor[time.Duration]() {
		applyDurationDefault := func(reason string) {
			envTag := fieldType.Tag.Get("env")
			defaultValue := fieldType.Tag.Get("default")

			if fallback, fallbackErr := time.ParseDuration(defaultValue); fallbackErr == nil {
				slog.Warn("Invalid duration for config field, using tagged default", //nolint:gosec // logging invalid config input for diagnostics is intentional here.
					"reason", reason,
					"field", envTag,
					"value", value,
					"default", defaultValue)
				field.SetInt(int64(fallback))
			} else {
				slog.Warn("Invalid duration for config field and invalid tagged default", //nolint:gosec // logging invalid config input for diagnostics is intentional here.
					"reason", reason,
					"field", envTag,
					"value", value,
					"default", defaultValue)
			}
		}

		if d, err := time.ParseDuration(value); err == nil {
			if d > 0 {
				field.SetInt(int64(d))
			} else {
				applyDurationDefault("Non-positive duration for config field")
			}
		} else {
			applyDurationDefault("Invalid duration for config field")
		}
		return
	}

	// Handle custom types based on underlying kind
	if field.Type().ConvertibleTo(reflect.TypeFor[string]()) {
		// String-based types like AppEnvironment
		field.Set(reflect.ValueOf(value).Convert(field.Type()))
	} else if field.Type() == reflect.TypeFor[os.FileMode]() {
		// os.FileMode
		if i, err := strconv.ParseUint(value, 8, 32); err == nil {
			field.Set(reflect.ValueOf(os.FileMode(i)))
		}
	}
}

func trimQuotes(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

func (a AppEnvironment) IsProdEnvironment() bool {
	return a == AppEnvironmentProduction
}

func (a AppEnvironment) IsTestEnvironment() bool {
	return a == AppEnvironmentTest
}

// ListenAddr returns the effective address for the HTTP server to bind to.
// It uses LISTEN as the host (if set) and PORT for the port.
func (c *Config) ListenAddr() string {
	host := strings.TrimSpace(c.Listen)
	port := c.Port
	if port == "" {
		port = "3552"
	}
	if host == "" {
		return ":" + port
	}
	return net.JoinHostPort(host, port)
}

// GetAppURL returns the effective application URL.
// If in agent mode and APP_URL is not explicitly set, it returns the manager's URL.
func (c *Config) GetAppURL() string {
	// If APP_URL is explicitly set to something other than the default, use it
	if os.Getenv("APP_URL") != "" {
		return c.AppUrl
	}

	return c.AppUrl
}

// MaskSensitive returns a copy of the config with sensitive fields masked.
// Useful for logging configuration without exposing secrets.
func (c *Config) MaskSensitive() map[string]any {
	result := make(map[string]any)
	v := reflect.ValueOf(c).Elem()
	t := v.Type()

	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		fieldType := t.Field(i)

		envTag := fieldType.Tag.Get("env")
		if envTag == "" {
			envTag = fieldType.Name
		}

		// Fields with "file" option are considered sensitive
		optionsTag := fieldType.Tag.Get("options")
		isSensitive := strings.Contains(optionsTag, "file")

		if isSensitive {
			// Mask sensitive values
			strVal := fmt.Sprintf("%v", field.Interface())
			if len(strVal) > 0 {
				result[envTag] = "****"
			} else {
				result[envTag] = "(empty)"
			}
		} else {
			result[envTag] = field.Interface()
		}
	}

	return result
}
