// Package mattermost implements a mattermost adapter for the joe bot library.
package mattermost

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"github.com/pkg/errors"

	"go.uber.org/zap"

	"github.com/go-joe/joe"
	"github.com/mattermost/mattermost-server/model"
)

//
//const bufsz = 10

// BotAdapter implements a joe.Adapter that reads and writes messages to and
// from Mattermost.
type BotAdapter struct {
	context context.Context
	logger  *zap.Logger
	user    *model.User
	team    *model.Team

	api mattermostAPI

	rooms            map[string]*model.Channel
	roomsMu          sync.RWMutex
	roomNamesFromIDs map[string]string
}

// Config contains the configuration of a BotAdapter.
type Config struct {
	Token     string
	Team      string
	ServerURL *url.URL
	Name      string
	Logger    *zap.Logger
}

type mattermostAPI interface {
	CreatePost(post *model.Post) (*model.Post, *model.Response)
	//SaveReaction(reaction *model.Reaction) (*model.Reaction, *model.Response)
	GetMe(etag string) (*model.User, *model.Response)
	EventStream() chan *model.WebSocketEvent
	GetChannelByName(channelName, teamId string, etag string) (*model.Channel, *model.Response)
	GetChannel(channelId, etag string) (*model.Channel, *model.Response)
	GetTeamByName(name, etag string) (*model.Team, *model.Response)
	Close() error
}

//Adapter returns a new mattermost Adapter as joe.Module.
func Adapter(token, serverURL, teamName string, opts ...Option) joe.Module {
	return joe.ModuleFunc(func(joeConf *joe.Config) error {
		conf, err := newConf(token, serverURL, teamName, joeConf, opts)
		if err != nil {
			return err
		}

		a, err := NewAdapter(joeConf.Context, conf)
		if err != nil {
			return err
		}

		joeConf.SetAdapter(a)
		return nil
	})
}

func newConf(token, serverURL, teamName string, joeConf *joe.Config, opts []Option) (Config, error) {
	u, err := url.Parse(serverURL)
	if err != nil {
		return Config{}, err
	}
	conf := Config{Token: token, ServerURL: u, Name: joeConf.Name, Team: teamName}

	for _, opt := range opts {
		err := opt(&conf)
		if err != nil {
			return conf, err
		}
	}

	if conf.Logger == nil {
		conf.Logger = joeConf.Logger("mattermost")
	}

	return conf, nil
}

// NewAdapter creates a new *BotAdapter that connects to mattermost. Note that you
// will usually configure the mattermost adapter as joe.Module (i.e. using the
// Adapter function of this package).
func NewAdapter(ctx context.Context, conf Config) (*BotAdapter, error) {
	uri := conf.ServerURL.String()
	wsURL, _ := url.Parse(uri)
	wsURL.Scheme = "wss"

	wsClient, err := model.NewWebSocketClient4(wsURL.String(), conf.Token)
	if err != nil {
		return nil, err
	}

	client := &mmClient{
		Client4:  model.NewAPIv4Client(conf.ServerURL.String()),
		wsClient: wsClient,
	}
	client.SetToken(conf.Token)

	return newAdapter(ctx, client, conf)
}

func newAdapter(ctx context.Context, client mattermostAPI, conf Config) (*BotAdapter, error) {
	user, appErr := client.GetMe("")
	if appErr.Error != nil {
		return nil, errors.Wrapf(appErr.Error, "error getting self")
	}

	a := &BotAdapter{
		context:          ctx,
		logger:           conf.Logger,
		user:             user,
		api:              client,
		rooms:            make(map[string]*model.Channel),
		roomNamesFromIDs: make(map[string]string),
	}

	if team, err := client.GetTeamByName(conf.Team, ""); err != nil {
		a.logger.Error("unable to find team, are you sure the bot is a member?",
			zap.String("team", conf.Team),
			zap.Error(err.Error),
		)
		return nil, err.Error
	} else {
		a.team = team
	}

	if a.logger == nil {
		a.logger = zap.NewNop()
	}

	a.logger.Info("Connected to mattermost API",
		zap.String("url", conf.ServerURL.String()),
		zap.String("username", a.user.Username),
		zap.String("id", a.user.Id),
		zap.String("team", a.team.Name),
	)
	return a, nil
}

// RegisterAt implements the joe.Adapter interface by emitting the mattermost API
// events to the given brain.
func (a *BotAdapter) RegisterAt(brain *joe.Brain) {
	go a.handleEvents(brain)
}

func (a *BotAdapter) handleEvents(brain *joe.Brain) {
	evts := a.api.EventStream()
waitloop:
	for evts != nil {
		select {
		case evt, ok := <-evts:
			if !ok {
				evts = nil
				continue
			}
			switch evt.Event {
			case model.WEBSOCKET_EVENT_POSTED:
				a.handleMessageEvent(evt, brain)
			default:
			}
		case <-a.context.Done():
			break waitloop
		}
	}
}

func (a *BotAdapter) handleMessageEvent(msg *model.WebSocketEvent, brain *joe.Brain) {
	post := model.PostFromJson(strings.NewReader(msg.Data["post"].(string)))
	if post == nil {
		a.logger.Error("Unable to parse post", zap.String("data", msg.Data["post"].(string)))
		return
	}

	// Short-circuit for our own messages
	if post.UserId == a.user.Id {
		return
	}

	channel := a.roomsByID(post.ChannelId)
	if channel == nil {
		return
	}
	direct := channel.Type == model.CHANNEL_DIRECT
	// check if we have a DM, or standard channel post
	selfLink := a.userLink(a.user.Username)
	if !direct && !strings.Contains(post.Message, selfLink) {
		// Message isn't for us, exiting
		return
	}

	text := strings.TrimSpace(strings.TrimPrefix(post.Message, selfLink))
	brain.Emit(joe.ReceiveMessageEvent{
		Text:     text,
		Channel:  channel.Name,
		AuthorID: post.UserId,
		Data:     post,
		ID:       post.Id,
	})
}

func (a *BotAdapter) roomsByID(rid string) *model.Channel {
	a.roomsMu.RLock()
	roomName, ok := a.roomNamesFromIDs[rid]
	a.roomsMu.RUnlock()
	if ok {
		return a.roomsByName(roomName)
	}

	a.roomsMu.Lock()
	defer a.roomsMu.Unlock()
	ch, resp := a.api.GetChannel(rid, "")
	if resp != nil {
		a.logger.Error("Received error from GetChannel",
			zap.String("rid", rid),
			zap.Error(resp.Error),
		)
		return nil
	}
	a.rooms[ch.Name] = ch
	a.roomNamesFromIDs[rid] = ch.Name
	return ch

}

func (a *BotAdapter) roomsByName(name string) *model.Channel {
	a.roomsMu.RLock()
	room, ok := a.rooms[name]
	a.roomsMu.RUnlock()
	if ok {
		return room
	}
	a.roomsMu.Lock()
	defer a.roomsMu.Unlock()

	// It's possible the room was filled in by another thread while waiting
	// for write lock.
	room, ok = a.rooms[name]
	if ok {
		return room
	}

	ch, resp := a.api.GetChannelByName(name, a.team.Id, "")
	if resp != nil {
		a.logger.Error("Received error from GetChannelByName",
			zap.String("name", name),
			zap.Error(resp.Error),
		)
		return nil
	}
	a.rooms[name] = ch
	return ch
}

// Send implements joe.Adapter by sending all received text messages to the
// given mattermost channel name.
func (a *BotAdapter) Send(text, channelName string) error {

	room := a.roomsByName(channelName)
	if room == nil {
		a.logger.Error("Could not send message, channel not found",
			zap.String("channelName", channelName),
		)
		return fmt.Errorf("could not send message, channel '%s' not found", channelName)
	}

	p := &model.Post{Message: text, ChannelId: room.Id}

	a.logger.Info("Sending message to channel",
		zap.String("channelName", channelName),
		// do not leak actual message content since it might be sensitive
	)
	_, resp := a.api.CreatePost(p)
	if resp != nil {
		a.logger.Error("unable to create post", zap.Error(resp.Error))
		return errors.Wrap(resp.Error, "unable to create post")
	}

	return nil
}

// Close disconnects the adapter from the mattermost API.
func (a *BotAdapter) Close() error {
	return a.api.Close()
}

// userLink takes a username and returns the formatting necessary to link it.
func (a *BotAdapter) userLink(username string) string {
	return fmt.Sprintf("@%s", username)
}

//
//// newMessage creates basic message with an ID, a RoomID, and a Msg
//// Takes channel and text
//func (a *BotAdapter) newMessage(channel *models.Channel, text string) *models.Message {
//	return &models.Message{
//		ID:     a.idgen.ID(),
//		RoomID: channel.ID,
//		Msg:    text,
//		User:   a.user,
//	}
//}
//
//func (a *BotAdapter) React(r reactions.Reaction, msg joe.Message) error {
//	m := &models.Message{ID: msg.ID}
//	err := a.rocket.ReactToMessage(m, ":"+r.Shortcode+":")
//	if err != nil {
//		return errors.Wrapf(err, "Error reacting to message: msg: %s, reaction: %s", msg.ID, r.Shortcode)
//	}
//	return nil
//}
