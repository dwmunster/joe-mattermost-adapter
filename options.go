package mattermost

import (
	"go.uber.org/zap"
)

// An Option is used to configure the mattermost adapter.
type Option func(*Config) error

// WithLogger can be used to inject a different logger for the mattermost adapater.
func WithLogger(logger *zap.Logger) Option {
	return func(conf *Config) error {
		conf.Logger = logger
		return nil
	}
}
