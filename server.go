package lrcd

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"slices"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/gorilla/websocket"
	"github.com/rachel-mp4/lrcproto/gen/go"
	"google.golang.org/protobuf/proto"
)

type Server struct {
	secret        string
	uri           string
	eventBus      chan clientEvent
	ctx           context.Context
	cancel        context.CancelFunc
	clients       map[*client]bool
	clientsMu     sync.Mutex
	idmapsMu      sync.Mutex
	idToClient    map[uint32]*client
	exidToClients map[string][]*client
	exidtocmu     sync.Mutex
	lastID        uint32
	logger        *log.Logger
	debugLogger   *log.Logger
	welcomeEvt    []byte
	pongEvt       []byte
	initChan      chan InitChanMsg
	mediainitChan chan MediaInitChanMsg
	resolver      func(externalID string, ctx context.Context) *string
	pubChan       chan PubEvent
	allocateID    func() uint32
	cseid         bool //consumer sets external id
	slowcleanup   *time.Duration
}

type PubEvent struct {
	ID   uint32
	Body string
}

type client struct {
	conn      *websocket.Conn
	dataChan  chan []byte
	ctx       context.Context
	cancel    context.CancelFunc
	muteMap   map[*client]bool
	mutedBy   map[*client]bool
	myIDs     []uint32
	init      *lrcpb.Init
	mediainit *lrcpb.MediaInit
	text      []uint16
	nick      *string
	externID  *string
	resolvID  *string
	rcancel   context.CancelFunc
	color     *uint32
	mu        sync.Mutex
}

func (c *client) body() string {
	t := utf16.Decode(c.text)
	return string(t)
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
	if options.mediainitChan != nil {
		s.mediainitChan = options.mediainitChan
	}
	if options.pubChan != nil {
		s.pubChan = options.pubChan
	}
	if options.allocateID != nil {
		s.allocateID = options.allocateID
	} else {
		s.allocateID = func() uint32 {
			return s.lastID + 1
		}
	}
	if options.initialID != nil {
		s.lastID = *options.initialID
	}
	if options.slowcleanup != nil {
		s.slowcleanup = options.slowcleanup
	}
	s.uri = options.uri
	s.secret = options.secret
	s.resolver = options.resolver
	s.cseid = options.cseid

	s.clients = make(map[*client]bool)
	s.exidToClients = make(map[string][]*client)
	s.clientsMu = sync.Mutex{}
	s.idmapsMu = sync.Mutex{}
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
		if s.initChan != nil {
			close(s.initChan)
		}
		if s.pubChan != nil {
			close(s.pubChan)
		}
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

func (s *Server) SendReply(reply *lrcpb.Event_Attachreply) {
	event := lrcpb.Event{Msg: reply}
	s.broadcast(&event, nil)
}

func (s *Server) SendReplyBatch(replies *lrcpb.Event_Replybatch) {
	event := lrcpb.Event{Msg: replies}
	s.broadcast(&event, nil)
}

var ErrIDDNE = errors.New("provided id does not exist")
var ErrIDDone = errors.New("provided id has been published")

// GetExternIDAndBodyFrom returns the current externalId and post body from an in-progress
// message id. If the message has been finished, it will just return the externalId. since
// external id can be reset at any time in default configuration, this provides most valuable
// information when used WithConsumerSetsExternalID option
func (s *Server) GetExternIDAndBodyFrom(id uint32) (extern *string, body *string, err error) {
	s.idmapsMu.Lock()
	defer s.idmapsMu.Unlock()
	c, ok := s.idToClient[id]
	if !ok {
		return nil, nil, ErrIDDNE
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.externID != nil {
		e := *c.externID
		extern = &e
	}
	if c.text != nil && c.init != nil && c.init.Id != nil && *c.init.Id == id {
		b := c.body()
		body = &b
	} else {
		err = ErrIDDone
	}
	return
}

func (s *Server) wshandler(externalId *string) http.HandlerFunc {
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

		if netConn := conn.UnderlyingConn(); netConn != nil {
			if tcpConn, ok := netConn.(*net.TCPConn); ok {
				if err := tcpConn.SetNoDelay(true); err != nil {
					log.Println("failed to denagle")
				}
			}
		}

		ctx, cancel := context.WithCancel(context.Background())
		cli := &client{
			conn:     conn,
			dataChan: make(chan []byte, 100),
			ctx:      ctx,
			cancel:   cancel,
			muteMap:  make(map[*client]bool, 0),
			mutedBy:  make(map[*client]bool, 0),
			myIDs:    make([]uint32, 0, 8),
			externID: externalId,
			text:     make([]uint16, 0, 16),
		}

		s.clientsMu.Lock()
		s.clients[cli] = true
		s.clientsMu.Unlock()
		if externalId != nil {
			s.exidtocmu.Lock()
			clis := s.exidToClients[*externalId]
			s.exidToClients[*externalId] = append(clis, cli)
			s.exidtocmu.Unlock()
			if len(clis) > 20 {
				s.log(fmt.Sprintf("that's a lot of clients: %s", *externalId))
			}
		}

		// lifetime
		s.logDebug("new ws connection!")
		go s.wsReader(cli)
		s.wsWriter(cli)

		// clean up
		s.clientsMu.Lock()
		delete(s.clients, cli)
		s.clientsMu.Unlock()
		// it's better to send on event bus to avoid race conditions
		s.eventBus <- clientEvent{cli, &lrcpb.Event{Msg: &lrcpb.Event_Pub{Pub: &lrcpb.Pub{}}}}
		s.eventBus <- clientEvent{cli, &lrcpb.Event{Msg: &lrcpb.Event_Mediapub{Mediapub: &lrcpb.MediaPub{}}}}
		if cli.externID != nil {
			s.exidtocmu.Lock()
			clis := s.exidToClients[*cli.externID]
			s.exidToClients[*cli.externID] = slices.DeleteFunc(clis, func(c *client) bool {
				return cli == c
			})
			s.exidtocmu.Unlock()
		}

		if s.slowcleanup != nil {
			time.Sleep(*s.slowcleanup)
		}

		s.idmapsMu.Lock()
		for _, id := range cli.myIDs { // remove myself from the idToClient map
			delete(s.idToClient, id)
		}
		for mutedClient := range cli.muteMap { // remove myself from everyone that I muted's backreference map
			delete(mutedClient.mutedBy, cli)
		}
		for mutingClient := range cli.mutedBy { // remove myself from everyone who muted me
			delete(mutingClient.muteMap, cli)
		}
		s.idmapsMu.Unlock()

		s.logDebug("closed ws connection")
	}
}

func (s *Server) WSHandler() http.HandlerFunc {
	if s.cseid {
		panic("server created with ConsumerSetsExternalID, so use WSHandlerExternal method")
	}
	return s.wshandler(nil)
}

func (s *Server) WSHandlerExternal(externalId string) http.HandlerFunc {
	if !s.cseid {
		return s.wshandler(nil)
	}
	return s.wshandler(&externalId)
}

func (s *Server) KickExternalId(externalId string) {
	s.exidtocmu.Lock()
	clis, ok := s.exidToClients[externalId]
	s.exidtocmu.Unlock()
	if !ok {
		return
	}
	e := &lrcpb.Event{Msg: &lrcpb.Event_Kick{Kick: &lrcpb.Kick{Privileges: &lrcpb.Sudo{Secret: s.secret}}}}
	for _, cli := range clis {
		select {
		case s.eventBus <- clientEvent{cli, e}:
		default:
			cli.cancel()
		}
	}
}

func (s *Server) wsReader(client *client) {
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
			s.eventBus <- clientEvent{client: client, event: &event}
		}
	}
}

func (s *Server) wsWriter(client *client) {
	defer client.conn.Close()
	defer client.conn.WriteControl(websocket.CloseMessage, nil, time.Now().Add(5*time.Second))
	ticker := time.NewTicker(15 * time.Second)
	for {
		select {
		case <-ticker.C:
			err := client.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
			if err != nil {
				client.cancel()
				return
			}
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
			case *lrcpb.Event_Mediainit:
				s.handleMediainit(msg, client)
			case *lrcpb.Event_Pub:
				s.handlePub(client)
			case *lrcpb.Event_Mediapub:
				s.handleMediapub(msg, client)
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
			case *lrcpb.Event_Editbatch:
				s.handleEditBatch(msg, client)
			case *lrcpb.Event_Attachreply:
				s.handleAttachReply(msg, client)
			case *lrcpb.Event_Detachreply:
				s.handleDetachReply(msg, client)
			case *lrcpb.Event_Kick:
				s.handleKick(msg, client)
			}
		}
	}
}

func (s *Server) handleInit(msg *lrcpb.Event_Init, client *client) {
	client.mu.Lock()
	init := client.init
	if init != nil {
		client.mu.Unlock()
		return
	}
	s.idmapsMu.Lock()
	newID := s.allocateID()
	s.lastID = newID
	s.idToClient[newID] = client
	s.idmapsMu.Unlock()
	client.myIDs = append(client.myIDs, newID)
	if client.text == nil {
		client.text = make([]uint16, 0, 16)
	} else {
		client.text = client.text[:0]
	}
	msg.Init.Id = &newID
	msg.Init.Nick = client.nick
	// right now i am lazy to add an actual field to set if a reply should be
	// anonymous, so user can explicitly set their external id to be empty string
	// (which should not be a valid external id in most applications)
	// for the time being
	// fix this later march 30 2026
	if msg.Init.ExternalID == nil || *msg.Init.ExternalID != "" {
		msg.Init.ExternalID = client.externID
	} else {
		msg.Init.ExternalID = nil
	}
	msg.Init.Color = client.color
	echoed := false
	msg.Init.Echoed = &echoed
	msg.Init.Nonce = nil
	client.init = msg.Init
	client.mu.Unlock()

	if s.initChan != nil {
		select {
		case s.initChan <- InitChanMsg{*msg, client.resolvID}:
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
	msg.Init.Nonce = GenerateNonce(*msg.Init.Id, s.uri, s.secret)
	echoEvent := &lrcpb.Event{Msg: msg}
	echoData, _ := proto.Marshal(echoEvent)
	msg.Init.Echoed = nil // unset fields since client has pointer to Init, which they
	msg.Init.Nonce = nil  // may broadcast on get.Item
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
func (s *Server) handleMediainit(msg *lrcpb.Event_Mediainit, client *client) {
	s.logDebug("want to handle media init")
	mediaInit := client.mediainit
	if mediaInit != nil {
		return
	}
	s.idmapsMu.Lock()
	s.logDebug("handling media init")
	newID := s.allocateID()
	s.lastID = newID
	s.idToClient[newID] = client
	s.idmapsMu.Unlock()
	client.mu.Lock()
	client.myIDs = append(client.myIDs, newID)
	msg.Mediainit.Id = &newID
	msg.Mediainit.Nick = client.nick
	msg.Mediainit.ExternalID = client.externID
	msg.Mediainit.Color = client.color
	client.mediainit = msg.Mediainit
	client.mu.Unlock()
	echoed := false
	msg.Mediainit.Echoed = &echoed
	msg.Mediainit.Nonce = nil
	if s.mediainitChan != nil {
		select {
		case s.mediainitChan <- MediaInitChanMsg{*msg, client.resolvID}:
		default:
			s.log("initchan blocked, closing channel")
			close(s.mediainitChan)
			s.mediainitChan = nil
		}
	}
	s.broadcastMediainit(msg, client)
}

func (s *Server) broadcastMediainit(msg *lrcpb.Event_Mediainit, client *client) {
	stdEvent := &lrcpb.Event{Msg: msg}
	stdData, _ := proto.Marshal(stdEvent)
	echoed := true
	msg.Mediainit.Echoed = &echoed
	msg.Mediainit.Nonce = GenerateNonce(*msg.Mediainit.Id, s.uri, s.secret)
	echoEvent := &lrcpb.Event{Msg: msg}
	echoData, _ := proto.Marshal(echoEvent)
	msg.Mediainit.Echoed = nil // unset fields now that we are done since client has pointer to Mediainit
	msg.Mediainit.Nonce = nil
	muteEvent := &lrcpb.Event{Msg: &lrcpb.Event_Mute{Mute: &lrcpb.Mute{Id: msg.Mediainit.GetId()}}}
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
			s.logDebug("b mediainit")
		default:
			s.log("kicked client")
			client.cancel()
		}
	}
}

func (s *Server) handlePub(client *client) {
	client.mu.Lock()
	init := client.init
	if init == nil {
		client.mu.Unlock()
		return
	}
	curID := init.Id
	if curID == nil {
		client.mu.Unlock()
		return
	}
	client.init = nil
	event := &lrcpb.Event{Msg: &lrcpb.Event_Pub{Pub: &lrcpb.Pub{Id: curID}}}
	if s.pubChan != nil {
		select {
		case s.pubChan <- PubEvent{ID: *curID, Body: client.body()}:
		default:
			s.log("pubchan blocked, closing channel")
			close(s.pubChan)
			s.pubChan = nil
		}
	}
	if cap(client.text) > 1024 { // idea is that in most cases it's ok to keep
		client.text = nil // the client's text the same size as they've been typing
	} else { // but 1024+ utf16 characters is a lot so likely they just pasted
		client.text = client.text[:0] // a long thing and we can shrink their text
	}
	client.mu.Unlock()
	s.broadcast(event, client)
}

func (s *Server) handleMediapub(msg *lrcpb.Event_Mediapub, client *client) {
	if msg == nil || msg.Mediapub == nil {
		return
	}
	client.mu.Lock()
	cid := client.mediainit
	if cid == nil {
		client.mu.Unlock()
		return
	}
	curID := cid.Id
	if curID == nil {
		client.mu.Unlock()
		return
	}
	client.mediainit = nil
	client.mu.Unlock()
	msg.Mediapub.Id = curID
	body := "external media."
	if msg.Mediapub.Alt != nil {
		body += fmt.Sprintf(" alt=%s.", *msg.Mediapub.Alt)
	}
	if msg.Mediapub.ContentAddress != nil {
		body += fmt.Sprintf(" cid=%s.", *msg.Mediapub.ContentAddress)
	}
	if s.pubChan != nil {
		select {
		case s.pubChan <- PubEvent{ID: *curID, Body: body}:
		default:
			s.log("pubchan blocked, closing channel")
			close(s.pubChan)
			s.pubChan = nil
		}
	}
	event := &lrcpb.Event{Msg: msg}
	s.broadcast(event, client)
}

func (s *Server) handleInsert(msg *lrcpb.Event_Insert, client *client) {
	client.mu.Lock()
	init := client.init
	if init == nil {
		client.mu.Unlock()
		return
	}
	curID := init.Id
	if curID == nil {
		client.mu.Unlock()
		return
	}
	err := client.applyInsert(msg.Insert.GetUtf16Index(), msg.Insert.GetBody())
	if err != nil {
		client.mu.Unlock()
		return
	}
	client.mu.Unlock()
	msg.Insert.Id = curID
	event := &lrcpb.Event{Msg: msg}
	s.broadcast(event, client)
}

func (c *client) applyInsert(index uint32, s string) error {
	idx := int(index)
	runes := []rune(s)
	su16 := utf16.Encode(runes)
	su16len := len(su16)
	ltext := len(c.text)
	if ltext < idx { // inserting outside the bounds of the string, not allowed
		return errors.New("index out of range")
	} else if ltext == idx { // inserting just at the bounds of the string, ezpz
		c.text = append(c.text, su16...)
		return nil
	} else { // inserting inside the bounds of the string, annoying cuz we have to copy stuff over
		ctext := cap(c.text)
		remainingCap := ctext - ltext
		mustGainAtLeast := su16len - remainingCap
		if mustGainAtLeast > 0 { // this stuff is here because we are manually growing to try and keep
			fgrow := ctext * 2 // heap allocations to a minimum, so we're picking the max of either the
			if ctext > 256 {   // standard grow algo (if cap < 256, double, otherwise x1.25), alongside
				fgrow = (ctext * 5) / 4 // the actual amount we need to gain, so if someone posts a long
			} // copypasta or whatever it only needs to do one heap allocation. might as well give some
			growamt := max(fgrow, (mustGainAtLeast*5)/4) // room afterwards in the mgal case since it's
			c.text = slices.Grow(c.text, growamt)        // likely they'll continue typing
		}
		res := c.text[:ltext+su16len]
		for i, v := range slices.Backward(c.text[idx:]) { // backwards so we don't overwrite data we
			res[idx+i+su16len] = v // care about, since res is still pointing at the same backing array
		}
		for i, v := range su16 {
			res[idx+i] = v
		}
		c.text = res
		return nil
	}
}

func (s *Server) handleDelete(msg *lrcpb.Event_Delete, client *client) {
	client.mu.Lock()
	init := client.init
	if init == nil {
		client.mu.Unlock()
		return
	}
	curID := init.Id
	if curID == nil {
		client.mu.Unlock()
		return
	}
	err := client.applyDelete(msg.Delete.GetUtf16Start(), msg.Delete.GetUtf16End())
	client.mu.Unlock()
	if err != nil {
		return
	}
	msg.Delete.Id = curID
	event := &lrcpb.Event{Msg: msg}
	s.broadcast(event, client)
}

func (c *client) applyDelete(start uint32, end uint32) error {
	if end <= start {
		return errors.New("end must come after start")
	}
	si := int(start)
	ei := int(end)
	ltext := len(c.text)
	if ltext < ei {
		return errors.New("index out of range")
	}
	for i, v := range c.text[ei:] { // thankfully delete is far simpler
		c.text[si+i] = v // than insert, the only weird thing here is the
	} // expression in the last line where we reslice to the new length:
	c.text = c.text[:ltext-ei+si] // initial - (end - start)
	return nil
}

func (s *Server) broadcast(event *lrcpb.Event, cli *client) {
	data, _ := proto.Marshal(event)
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	for c := range s.clients {
		if cli != nil && cli.mutedBy[c] {
			continue
		}
		select {
		case c.dataChan <- data:
			s.logDebug("b")
		default:
			s.log("kicked client")
			c.cancel()
		}
	}
}

func (s *Server) handleEditBatch(msg *lrcpb.Event_Editbatch, client *client) {
	client.mu.Lock()
	init := client.init
	if init == nil {
		client.mu.Unlock()
		return
	}
	curID := init.Id
	if curID == nil {
		client.mu.Unlock()
		return
	}
	for _, edit := range msg.Editbatch.Edits {
		switch edit := edit.Edit.(type) {
		case *lrcpb.Edit_Insert:
			err := client.applyInsert(edit.Insert.GetUtf16Index(), edit.Insert.GetBody())
			if err != nil {
				client.mu.Unlock()
				return
			}
		case *lrcpb.Edit_Delete:
			err := client.applyDelete(edit.Delete.GetUtf16Start(), edit.Delete.GetUtf16End())
			if err != nil {
				client.mu.Unlock()
				return
			}
		}
	}
	client.mu.Unlock()
	event := &lrcpb.Event{Msg: msg, Id: curID}
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
	defer s.idmapsMu.Unlock()
	clientToMute, ok := s.idToClient[toMute]
	if !ok {
		return
	}
	if clientToMute == client {
		return
	}
	clientToMute.mutedBy[client] = true
	client.muteMap[clientToMute] = true

}

func (s *Server) handleUnmute(msg *lrcpb.Event_Unmute, client *client) {
	toMute := msg.Unmute.GetId()
	s.idmapsMu.Lock()
	defer s.idmapsMu.Unlock()
	clientToMute, ok := s.idToClient[toMute]
	if !ok {
		return
	}
	if clientToMute == client {
		return
	}
	delete(clientToMute.mutedBy, client)
	delete(client.muteMap, clientToMute)
}

func (s *Server) handleSet(msg *lrcpb.Event_Set, cli *client) {
	cli.mu.Lock()
	defer cli.mu.Unlock()
	nick := msg.Set.Nick
	if nick != nil {
		nickname := *nick
		if len(nickname) <= 16 {
			cli.nick = &nickname
		}
	}
	if !s.cseid {
		externalId := msg.Set.ExternalID
		if externalId != nil {
			externid := *externalId
			s.exidtocmu.Lock()
			if cli.externID != nil {
				clis := s.exidToClients[*cli.externID]
				s.exidToClients[*cli.externID] = slices.DeleteFunc(clis, func(c *client) bool {
					return c == cli
				})
			}
			cli.externID = &externid
			clis := s.exidToClients[externid]
			s.exidToClients[externid] = append(clis, cli)
			s.exidtocmu.Unlock()
			if cli.rcancel != nil {
				cli.rcancel()
			}
			if s.resolver != nil {
				go func() {
					ctx, cancel := context.WithCancel(cli.ctx)
					cli.rcancel = cancel
					resolvid := s.resolver(externid, ctx)
					cli.mu.Lock()
					cli.resolvID = resolvid
					cli.mu.Unlock()
				}()
			}
		}
	}
	color := msg.Set.Color
	if color != nil {
		c := *color
		if c <= 0xffffff {
			cli.color = &c
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

func (s *Server) handleAttachReply(msg *lrcpb.Event_Attachreply, client *client) {
	client.mu.Lock()
	init := client.init
	if init == nil {
		client.mu.Unlock()
		return
	}
	curId := init.Id
	if curId == nil {
		mediainit := client.mediainit
		if mediainit == nil {
			client.mu.Unlock()
			return
		}
		curId = mediainit.Id
		if curId == nil {
			client.mu.Unlock()
			return
		}
	}
	msg.Attachreply.From = curId
	event := lrcpb.Event{Msg: msg}
	s.broadcast(&event, client)
}

func (s *Server) handleDetachReply(msg *lrcpb.Event_Detachreply, client *client) {
	client.mu.Lock()
	init := client.init
	if init == nil {
		client.mu.Unlock()
		return
	}
	curId := init.Id
	if curId == nil {
		mediainit := client.mediainit
		if mediainit == nil {
			client.mu.Unlock()
			return
		}
		curId = mediainit.Id
		if curId == nil {
			client.mu.Unlock()
			return
		}
	}
	client.mu.Unlock()
	msg.Detachreply.From = curId
	event := lrcpb.Event{Msg: msg}
	s.broadcast(&event, client)
}

func (s *Server) handleKick(msg *lrcpb.Event_Kick, cli *client) {
	if msg.Kick != nil && msg.Kick.Privileges != nil && msg.Kick.Privileges.Secret == s.secret {
		kick := &lrcpb.Event{Msg: &lrcpb.Event_Kick{Kick: &lrcpb.Kick{}}}
		data, _ := proto.Marshal(kick)
		cli.dataChan <- data
		s.clientsMu.Lock()
		delete(s.clients, cli)
		close(cli.dataChan)
		s.clientsMu.Unlock()
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
