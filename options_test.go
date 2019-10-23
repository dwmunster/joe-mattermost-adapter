package mattermost

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/go-joe/joe"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func joeConf(t *testing.T) *joe.Config {
	joeConf := new(joe.Config)
	joeConf.Name = "testname"
	require.NoError(t, joe.WithLogger(zaptest.NewLogger(t)).Apply(joeConf))
	return joeConf
}

func TestDefaultConfig(t *testing.T) {
	conf, err := newConf("fake@email", "password", "url", joeConf(t), []Option{})
	require.NoError(t, err)
	assert.NotNil(t, conf.Logger)
	assert.Equal(t, "testname", conf.Name)
}

func TestWithLogger(t *testing.T) {
	logger := zaptest.NewLogger(t)
	conf, err := newConf("fake@email", "password", "url", joeConf(t), []Option{
		WithLogger(logger),
	})

	require.NoError(t, err)
	assert.Equal(t, logger, conf.Logger)
}
