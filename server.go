package lrcd

import (
	"context"
	"errors"
	"github.com/gorilla/websocket"
	"github.com/rachel-mp4/lrcproto/gen/go"
	"google.golang.org/protobuf/proto"
	"log"
	"net/http"
	"sync"
	"unicode/utf16"
)

type Server struct {
	eventBus    chan clientEvent
	ctx         context.Context
	cancel      context.CancelFunc
	clients     map[*client]bool
	clientsMu   sync.Mutex
	idmapsMu    sync.Mutex
	clientToID  map[*client]*uint32
	idToClient  map[uint32]*client
	lastID      uint32
	logger      *log.Logger
	debugLogger *log.Logger
	welcomeEvt  []byte
	pongEvt     []byte
	initChan    chan lrcpb.Event_Init
	pubChan     chan PubEvent
}

type PubEvent struct {
	ID   uint32
	Body string
}

type client struct {
	conn     *websocket.Conn
	dataChan chan []byte
	ctx      context.Context
	cancel   context.CancelFunc
	muteMap  map[*client]bool
	mutedBy  map[*client]bool
	myIDs    []uint32
	post     *string
	nick     *string
	externID *string
	color    *uint32
}

type clientEvent struct {
	client *client
	event  *lrcpb.Event
}

func NewServer(opts ...Option) (*Server, error) {
	var options options
	for _, opt := range opts {
		err := opt(&options)
		if err != nil {
			return nil, err
		}
	}

	s := Server{}

	welcomeString := "Welcome to my lrc server!"
	if options.welcome != nil {
		welcomeString = *options.welcome
	}
	s.setDefaultEvents(welcomeString)

	if options.writer != nil {
		s.logger = log.New(*options.writer, "[log]", log.Ldate|log.Ltime)
		if options.verbose {
			s.debugLogger = log.New(*options.writer, "[debug]", log.Ldate|log.Ltime)
		}
	}

	if options.initChan != nil {
		s.initChan = options.initChan
	}
	if options.pubChan != nil {
		s.pubChan = options.pubChan
	}
	if options.initialID != nil {
		s.lastID = *options.initialID
	}

	s.clients = make(map[*client]bool)
	s.clientsMu = sync.Mutex{}
	s.idmapsMu = sync.Mutex{}
	s.clientToID = make(map[*client]*uint32)
	s.idToClient = make(map[uint32]*client)
	s.eventBus = make(chan clientEvent, 100)
	return &s, nil
}

func (s *Server) setDefaultEvents(welcome string) {
	evt := &lrcpb.Event{Msg: &lrcpb.Event_Get{Get: &lrcpb.Get{Topic: &welcome}}}
	we, _ := proto.Marshal(evt)
	s.welcomeEvt = we

	evt = &lrcpb.Event{Msg: &lrcpb.Event_Pong{Pong: &lrcpb.Pong{}}}
	pe, _ := proto.Marshal(evt)
	s.pongEvt = pe
}

// Start starts a server, and returns an error if it has ever been started before
func (s *Server) Start() error {
	if s.ctx != nil {
		return errors.New("cannot start already started server")
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	go s.broadcaster()
	s.logDebug("Hello, world!")
	return nil
}

// Stop stops a server if it has started, and returns an error if it is already stopped
func (s *Server) Stop() (uint32, error) {
	if s.ctx == nil {
		return s.lastID, nil
	}
	select {
	case <-s.ctx.Done():
		return s.lastID, errors.New("cannot stop already stopped server")
	default:
		s.cancel()
		s.logDebug("Goodbye world :c")
		return s.lastID, nil
	}
}

// Connected returns how many clients are currently connected to the server
func (s *Server) Connected() int {
	return len(s.clients)
}

// StopIfEmpty stops the server if it is empty, returning true.
func (s *Server) StopIfEmpty() bool {
	if len(s.clients) == 0 {
		s.Stop()
		return true
	}
	return false
}

func (s *Server) WSHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		upgrader := &websocket.Upgrader{
			Subprotocols: []string{"lrc.v1"},
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		}
		// initialize
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Println("Upgrade failed:", err)
			return
		}
		defer conn.Close()

		ctx, cancel := context.WithCancel(context.Background())
		client := &client{
			conn:     conn,
			dataChan: make(chan []byte, 100),
			ctx:      ctx,
			cancel:   cancel,
			muteMap:  make(map[*client]bool, 0),
			mutedBy:  make(map[*client]bool, 0),
			myIDs:    make([]uint32, 0),
		}

		s.clientsMu.Lock()
		s.clients[client] = true
		s.clientsMu.Unlock()

		// lifetime
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); s.wsWriter(client) }()
		go func() { defer wg.Done(); s.listenToWS(client) }()
		s.logDebug("new ws connection!")
		wg.Wait()

		// clean up
		s.clientsMu.Lock()
		delete(s.clients, client)
		close(client.dataChan)
		s.clientsMu.Unlock()

		s.idmapsMu.Lock()
		for _, id := range client.myIDs { // remove myself from the idToClient map
			delete(s.idToClient, id)
		}
		for mutedClient := range client.muteMap { // remove myself from everyone that I muted's backreference map
			delete(mutedClient.mutedBy, client)
		}
		for mutingClient := range client.mutedBy { // remove myself from everyone who muted me
			delete(mutingClient.muteMap, client)
		}
		s.idmapsMu.Unlock()

		conn.Close()
		s.logDebug("closed ws connection")
	}
}

func (s *Server) listenToWS(client *client) {
	for {
		select {
		case <-client.ctx.Done():
			s.logDebug("exiting listenToWS: client done")
			return
		case <-s.ctx.Done():
			s.logDebug("exiting listenToWS: server done")
			return
		default:
			_, data, err := client.conn.ReadMessage()
			if err != nil {
				s.logDebug("canceling client: read error")
				client.cancel()
				return
			}
			var event lrcpb.Event
			err = proto.Unmarshal(data, &event)
			if err != nil {
				s.logDebug(err.Error())
				client.cancel()
				return
			}
			s.eventBus <- clientEvent{client, &event}
		}
	}
}

func (s *Server) wsWriter(client *client) {
	for {
		select {
		case <-client.ctx.Done():
			s.logDebug("exiting wsWriter: client done")
			return
		case <-s.ctx.Done():
			s.logDebug("exiting wsWriter: server done")
			return
		case data, ok := <-client.dataChan:
			if !ok {
				s.logDebug("canceling client: dataChan closed")
				client.cancel()
				return
			}
			err := client.conn.WriteMessage(websocket.BinaryMessage, data)
			if err != nil {
				s.logDebug(err.Error())
				client.cancel()
				return
			}
		}
	}
}

// broadcaster takes an event from the events channel, and broadcasts it to all the connected clients individual event channels
func (s *Server) broadcaster() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case ce := <-s.eventBus:
			client := ce.client
			event := ce.event
			switch msg := event.Msg.(type) {
			case *lrcpb.Event_Ping:
				client.dataChan <- s.pongEvt
			case *lrcpb.Event_Pong:
				continue
			case *lrcpb.Event_Init:
				s.handleInit(msg, client)
			case *lrcpb.Event_Pub:
				s.handlePub(msg, client)
			case *lrcpb.Event_Insert:
				s.handleInsert(msg, client)
			case *lrcpb.Event_Delete:
				s.handleDelete(msg, client)
			case *lrcpb.Event_Mute:
				s.handleMute(msg, client)
			case *lrcpb.Event_Unmute:
				s.handleUnmute(msg, client)
			case *lrcpb.Event_Set:
				s.handleSet(msg, client)
			case *lrcpb.Event_Get:
				s.handleGet(msg, client)
			}
		}
	}
}

func (s *Server) handleInit(msg *lrcpb.Event_Init, client *client) {
	curID := s.clientToID[client]
	if curID != nil {
		return
	}
	newID := s.lastID + 1
	s.lastID = newID
	s.idmapsMu.Lock()
	s.clientToID[client] = &newID
	s.idToClient[newID] = client
	s.idmapsMu.Unlock()
	client.myIDs = append(client.myIDs, newID)
	newpost := ""
	client.post = &newpost
	msg.Init.Id = &newID
	msg.Init.Nick = client.nick
	msg.Init.ExternalID = client.externID
	msg.Init.Color = client.color
	echoed := false
	msg.Init.Echoed = &echoed
	if s.initChan != nil {
		select {
		case s.initChan <- *msg:
		default:
			s.log("initchan blocked, closing channel")
			close(s.initChan)
			s.initChan = nil
		}
	}
	s.broadcastInit(msg, client)
}

func (s *Server) broadcastInit(msg *lrcpb.Event_Init, client *client) {
	stdEvent := &lrcpb.Event{Msg: msg}
	stdData, _ := proto.Marshal(stdEvent)
	echoed := true
	msg.Init.Echoed = &echoed
	echoEvent := &lrcpb.Event{Msg: msg}
	echoData, _ := proto.Marshal(echoEvent)
	muteEvent := &lrcpb.Event{Msg: &lrcpb.Event_Mute{Mute: &lrcpb.Mute{Id: msg.Init.GetId()}}}
	muteData, _ := proto.Marshal(muteEvent)
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	for c := range s.clients {
		var dts []byte
		if c == client {
			dts = echoData
		} else if client.mutedBy[c] {
			dts = muteData
		} else {
			dts = stdData
		}
		select {
		case c.dataChan <- dts:
			s.logDebug("b init")
		default:
			s.log("kicked client")
			client.cancel()
		}
	}
}

func (s *Server) handlePub(msg *lrcpb.Event_Pub, client *client) {
	curID := s.clientToID[client]
	if curID == nil {
		return
	}
	s.idmapsMu.Lock()
	s.clientToID[client] = nil
	s.idmapsMu.Unlock()
	msg.Pub.Id = curID
	event := &lrcpb.Event{Msg: msg}
	if s.pubChan != nil {
		select {
		case s.pubChan <- PubEvent{ID: *curID, Body: *client.post}:
		default:
			s.log("pubchan blocked, closing channel")
			close(s.pubChan)
			s.pubChan = nil
		}
	}
	client.post = nil
	s.broadcast(event, client)
}

func (s *Server) handleInsert(msg *lrcpb.Event_Insert, client *client) {
	curID := s.clientToID[client]
	if curID == nil {
		return
	}
	newpost, err := insertAtUTF16Index(*client.post, msg.Insert.GetUtf16Index(), msg.Insert.GetBody())
	if err != nil {
		return
	}
	client.post = &newpost
	msg.Insert.Id = curID
	event := &lrcpb.Event{Msg: msg}
	s.broadcast(event, client)
}

func insertAtUTF16Index(base string, index uint32, insert string) (string, error) {
	runes := []rune(base)
	baseUTF16Units := utf16.Encode(runes)
	if uint32(len(baseUTF16Units)) < index {
		return "", errors.New("index out of range")
	}

	insertRunes := []rune(insert)
	insertUTF16Units := utf16.Encode(insertRunes)
	result := make([]uint16, 0, len(baseUTF16Units)+len(insertUTF16Units))
	result = append(result, baseUTF16Units[:index]...)
	result = append(result, insertUTF16Units...)
	result = append(result, baseUTF16Units[index:]...)
	resultRunes := utf16.Decode(result)
	return string(resultRunes), nil
}

func (s *Server) handleDelete(msg *lrcpb.Event_Delete, client *client) {
	curID := s.clientToID[client]
	if curID == nil {
		return
	}
	newPost, err := deleteBtwnUTF16Indices(*client.post, msg.Delete.GetUtf16Start(), msg.Delete.GetUtf16End())
	if err != nil {
		return
	}
	client.post = &newPost
	msg.Delete.Id = curID
	event := &lrcpb.Event{Msg: msg}
	s.broadcast(event, client)
}

func deleteBtwnUTF16Indices(base string, start uint32, end uint32) (string, error) {
	if end <= start {
		return "", errors.New("end must come after start")
	}
	runes := []rune(base)
	baseUTF16Units := utf16.Encode(runes)
	if uint32(len(baseUTF16Units)) < end {
		return "", errors.New("index out of range")
	}
	result := make([]uint16, 0, uint32(len(baseUTF16Units))+start-end)
	result = append(result, baseUTF16Units[:start]...)
	result = append(result, baseUTF16Units[end:]...)
	resultRunes := utf16.Decode(result)
	return string(resultRunes), nil
}

func (s *Server) broadcast(event *lrcpb.Event, client *client) {
	data, _ := proto.Marshal(event)
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	for c := range s.clients {
		if client.mutedBy[c] {
			continue
		}
		select {
		case c.dataChan <- data:
			s.logDebug("b")
		default:
			s.log("kicked client")
			client.cancel()
		}
	}
}

func (s *Server) handleMute(msg *lrcpb.Event_Mute, client *client) {
	toMute := msg.Mute.GetId()
	s.idmapsMu.Lock()
	clientToMute, ok := s.idToClient[toMute]
	if !ok {
		return
	}
	if clientToMute == client {
		return
	}
	clientToMute.mutedBy[client] = true
	client.muteMap[clientToMute] = true
	s.idmapsMu.Unlock()

}

func (s *Server) handleUnmute(msg *lrcpb.Event_Unmute, client *client) {
	toMute := msg.Unmute.GetId()
	s.idmapsMu.Lock()
	clientToMute, ok := s.idToClient[toMute]
	if !ok {
		return
	}
	if clientToMute == client {
		return
	}
	delete(clientToMute.mutedBy, client)
	delete(client.muteMap, clientToMute)
	s.idmapsMu.Unlock()

}

func (s *Server) handleSet(msg *lrcpb.Event_Set, client *client) {
	nick := msg.Set.Nick
	if nick != nil {
		nickname := *nick
		if len(nickname) <= 16 {
			client.nick = &nickname
		}
	}
	externalId := msg.Set.ExternalID
	if externalId != nil {
		externid := *externalId
		client.externID = &externid
	}
	color := msg.Set.Color
	if color != nil {
		c := *color
		if c <= 0xffffff {
			client.color = &c
		}
	}
}

func (s *Server) handleGet(msg *lrcpb.Event_Get, client *client) {
	t := msg.Get.Topic
	if t != nil {
		client.dataChan <- s.welcomeEvt
	}
	c := msg.Get.Connected
	if c != nil {
		conncount := uint32(len(s.clients))
		e := &lrcpb.Event{Msg: &lrcpb.Event_Get{Get: &lrcpb.Get{Connected: &conncount}}}
		data, _ := proto.Marshal(e)
		client.dataChan <- data
	}
}

// logDebug debugs unless in production
func (server *Server) logDebug(s string) {
	if server.debugLogger != nil {
		server.debugLogger.Println(s)
	}
}

func (server *Server) log(s string) {
	if server.logger != nil {
		server.logger.Println(s)
	}
}
