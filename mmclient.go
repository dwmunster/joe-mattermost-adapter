package mattermost

import (
	"net/url"

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

func (c *mmClient) Login(loginID string, password string) (*model.User, *model.Response) {
	user, resp := c.Client4.Login(loginID, password)
	wsURL, err := url.Parse(c.Url)
	if err != nil {
		return nil, nil
	}
	wsURL.Scheme = "wss"
	wsClient, appErr := model.NewWebSocketClient4(wsURL.String(), c.AuthToken)
	if appErr != nil {
		return nil, model.BuildErrorResponse(nil, appErr)
	}
	c.wsClient = wsClient

	return user, resp
}

func (c *mmClient) Close() error {
	c.wsClient.Close()
	ok, resp := c.Client4.Logout()
	if !ok {
		return resp.Error
	}
	return nil
}
