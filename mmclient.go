package mattermost

import (
	"github.com/mattermost/mattermost-server/model"
)

type mmClient struct {
	*model.Client4
	wsClient  *model.WebSocketClient
	listening bool
}

func (c *mmClient) EventStream() chan *model.WebSocketEvent {
	if c.wsClient == nil {
		return nil
	}
	if !c.listening {
		c.wsClient.Listen()
		c.listening = true
	}
	return c.wsClient.EventChannel
}

func (c *mmClient) Close() error {
	c.wsClient.Close()
	ok, resp := c.Client4.Logout()
	if !ok {
		return resp.Error
	}
	return nil
}
