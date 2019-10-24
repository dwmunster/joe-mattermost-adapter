package mattermost

import (
	"context"
	"fmt"
	"net/url"
	"testing"

	"github.com/go-joe/joe/joetest"
	"github.com/stretchr/testify/assert"

	"github.com/go-joe/joe"
	"github.com/mattermost/mattermost-server/model"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

var dummyUser = &model.User{
	Id:       "123",
	Username: "dummy_user",
}

var botUser = &model.User{
	Id:       "testID",
	Username: "testname",
}

var dummyRoom = model.Channel{
	Id:   "room0",
	Name: "Room 0",
	Type: model.CHANNEL_PRIVATE,
}

var dummyDM = model.Channel{
	Id:   "dm0",
	Name: "DM",
	Type: model.CHANNEL_DIRECT,
}

var dummyTeam = model.Team{
	Id:   "789",
	Name: "Awesome Team",
}

// compile time test to check if we are implementing the interface.
var _ joe.Adapter = new(BotAdapter)

func newTestAdapter(t *testing.T) (*BotAdapter, *mockMM) {
	ctx := context.Background()
	logger := zaptest.NewLogger(t)
	client := new(mockMM)
	client.evts = make(chan *model.WebSocketEvent)

	conf := Config{
		LoginID:  "test@email.com",
		Password: "123",
		Name:     "Test Name",
		Logger:   logger,
		Team:     dummyTeam.Name,
	}

	loginResp := botUser
	client.On("Login", conf.LoginID, conf.Password).Return(loginResp, nil)
	client.On("GetTeamByName", dummyTeam.Name, "").Return(&dummyTeam, nil)
	u, _ := url.Parse("https://nowhere")
	conf.ServerURL = u

	a, err := newAdapter(ctx, client, conf)
	require.NoError(t, err)

	return a, client
}

func TestAdapter_IgnoreNormalMessages(t *testing.T) {

	brain := joetest.NewBrain(t)
	a, api := newTestAdapter(t)
	api.On("EventStream").Return(api.evts)
	api.On("GetChannel", dummyRoom.Id, "").Return(&dummyRoom, nil)

	done := make(chan bool)
	go func() {
		a.handleEvents(brain.Brain)
		done <- true
	}()

	p := model.Post{
		Id:        "0",
		ChannelId: dummyRoom.Id,
		Message:   "Hello",
		UserId:    dummyUser.Id,
	}

	d := make(map[string]interface{})
	d["post"] = p.ToJson()

	api.evts <- &model.WebSocketEvent{
		Event:     model.WEBSOCKET_EVENT_POSTED,
		Data:      d,
		Broadcast: nil,
		Sequence:  0,
	}

	close(api.evts)
	<-done
	brain.Finish()

	assert.Empty(t, brain.RecordedEvents())
}

func TestAdapter_DirectMessages(t *testing.T) {
	brain := joetest.NewBrain(t)
	a, api := newTestAdapter(t)
	api.On("EventStream").Return(api.evts)
	api.On("GetChannel", dummyDM.Id, "").Return(&dummyDM, nil)

	done := make(chan bool)
	go func() {
		a.handleEvents(brain.Brain)
		done <- true
	}()

	p := model.Post{
		Id:        "0",
		ChannelId: dummyDM.Id,
		Message:   "Hello",
		UserId:    dummyUser.Id,
	}

	d := make(map[string]interface{})
	d["post"] = p.ToJson()

	api.evts <- &model.WebSocketEvent{
		Event:     model.WEBSOCKET_EVENT_POSTED,
		Data:      d,
		Broadcast: nil,
		Sequence:  0,
	}

	close(api.evts)
	<-done
	brain.Finish()

	events := brain.RecordedEvents()
	require.NotEmpty(t, events)
	expectedEvt := joe.ReceiveMessageEvent{Text: "Hello", Channel: dummyDM.Name, Data: &p, AuthorID: dummyUser.Id, ID: "0"}
	assert.Equal(t, expectedEvt, events[0])
}

func TestAdapter_MentionBot(t *testing.T) {
	brain := joetest.NewBrain(t)
	a, api := newTestAdapter(t)
	api.On("EventStream").Return(api.evts)
	api.On("GetChannel", dummyRoom.Id, "").Return(&dummyRoom, nil)

	done := make(chan bool)
	go func() {
		a.handleEvents(brain.Brain)
		done <- true
	}()

	p := model.Post{
		Id:        "0",
		ChannelId: dummyRoom.Id,
		Message:   fmt.Sprintf("hey %s do stuff", a.userLink(a.user.Username)),
		UserId:    dummyUser.Id,
	}

	d := make(map[string]interface{})
	d["post"] = p.ToJson()

	api.evts <- &model.WebSocketEvent{
		Event:     model.WEBSOCKET_EVENT_POSTED,
		Data:      d,
		Broadcast: nil,
		Sequence:  0,
	}

	close(api.evts)
	<-done
	brain.Finish()

	events := brain.RecordedEvents()
	require.NotEmpty(t, events)
	expectedEvt := joe.ReceiveMessageEvent{Text: p.Message, Channel: dummyRoom.Name, AuthorID: dummyUser.Id, Data: &p, ID: "0"}
	assert.Equal(t, expectedEvt, events[0])
}

func TestAdapter_MentionBotPrefix(t *testing.T) {
	brain := joetest.NewBrain(t)
	a, api := newTestAdapter(t)
	api.On("EventStream").Return(api.evts)
	api.On("GetChannel", dummyRoom.Id, "").Return(&dummyRoom, nil)

	done := make(chan bool)
	go func() {
		a.handleEvents(brain.Brain)
		done <- true
	}()

	p := model.Post{
		Id:        "0",
		ChannelId: dummyRoom.Id,
		Message:   fmt.Sprintf("%s do stuff", a.userLink(a.user.Username)),
		UserId:    dummyUser.Id,
	}

	d := make(map[string]interface{})
	d["post"] = p.ToJson()

	api.evts <- &model.WebSocketEvent{
		Event:     model.WEBSOCKET_EVENT_POSTED,
		Data:      d,
		Broadcast: nil,
		Sequence:  0,
	}

	close(api.evts)
	<-done
	brain.Finish()

	events := brain.RecordedEvents()
	require.NotEmpty(t, events)
	expectedEvt := joe.ReceiveMessageEvent{Text: "do stuff", Data: &p, AuthorID: dummyUser.Id, Channel: dummyRoom.Name, ID: "0"}
	assert.Equal(t, expectedEvt, events[0])
}

func TestAdapter_Send(t *testing.T) {
	a, api := newTestAdapter(t)
	api.On("GetChannelByName", dummyRoom.Name, dummyTeam.Id, "").Return(&dummyRoom, nil)

	p := &model.Post{
		ChannelId: dummyRoom.Id,
		Message:   "Hello World",
	}
	api.On("CreatePost", p).Return(&model.Post{}, nil)

	err := a.Send("Hello World", dummyRoom.Name)
	require.NoError(t, err)
	api.AssertExpectations(t)
}

func TestAdapter_Close(t *testing.T) {
	a, api := newTestAdapter(t)
	api.On("Close").Return(nil)

	err := a.Close()
	require.NoError(t, err)
	api.AssertExpectations(t)
}

type mockMM struct {
	mock.Mock
	evts chan *model.WebSocketEvent
}

//
var _ mattermostAPI = new(mockMM)

func (m *mockMM) CreatePost(p *model.Post) (post *model.Post, resp *model.Response) {
	args := m.Called(p)
	if x := args.Get(0); x != nil {
		post = x.(*model.Post)
	}
	if x := args.Get(1); x != nil {
		resp = x.(*model.Response)
	}

	return post, resp
}

func (m *mockMM) Login(loginId string, password string) (usr *model.User, resp *model.Response) {
	args := m.Called(loginId, password)
	if x := args.Get(0); x != nil {
		usr = x.(*model.User)
	}
	if x := args.Get(1); x != nil {
		resp = x.(*model.Response)
	}
	return usr, resp
}

func (m *mockMM) EventStream() chan *model.WebSocketEvent {
	args := m.Called()
	return args.Get(0).(chan *model.WebSocketEvent)
}

func (m *mockMM) Close() error {
	args := m.Called()
	return args.Error(0)
}

func (m *mockMM) GetChannelByName(channelName, teamID, etag string) (ch *model.Channel, resp *model.Response) {
	args := m.Called(channelName, teamID, etag)
	if x := args.Get(0); x != nil {
		ch = x.(*model.Channel)
	}
	if x := args.Get(1); x != nil {
		resp = x.(*model.Response)
	}
	return ch, resp
}

func (m *mockMM) GetChannel(id, etag string) (ch *model.Channel, resp *model.Response) {
	args := m.Called(id, etag)
	if x := args.Get(0); x != nil {
		ch = x.(*model.Channel)
	}
	if x := args.Get(1); x != nil {
		resp = x.(*model.Response)
	}
	return ch, resp
}

func (m *mockMM) GetTeamByName(name, etag string) (t *model.Team, resp *model.Response) {
	args := m.Called(name, etag)
	if x := args.Get(0); x != nil {
		t = x.(*model.Team)
	}
	if x := args.Get(1); x != nil {
		resp = x.(*model.Response)
	}
	return t, resp
}
