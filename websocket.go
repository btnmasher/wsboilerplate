package wsboilerplate

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// Time allowed to write a message to the peer.
	writeTimeout = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongTimeout = 60 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongTimeout * 9) / 10

	// Maximum message size allowed from peer.
	maxBytesDefault = 8192
)

type Message struct {
	Type  int
	Bytes []byte
}

type Server struct {
	maxBytes  int64
	wsBufSize int
	NewConn   chan *Connection
	upgrader  websocket.Upgrader
}

type Connection struct {
	websocket.Conn
	Send  chan *Message
	Recv  chan *Message
	End   chan struct{}
	Error chan error
}

// NewServer returns an instance of a WsServer configuration.
func NewServer(maxbytes int64, bufsize int, checkorigin bool) (error, *Server) {
	if maxbytes < 1 {
		return fmt.Errorf("Server Max Bytes cannot be less than 1, value given: %v", maxbytes), nil
	}

	u := websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
	}

	if !checkorigin {
		u.CheckOrigin = checkOriginStub
	}

	s := &Server{
		maxBytes:  maxbytes,
		wsBufSize: bufsize,
		NewConn:   make(chan *Connection, 10),
		upgrader:  u,
	}

	return nil, s
}

// ListenAndServeWs sets up a websocket endpoint handler then starts a webserver.
func (s *Server) ListenAndServeWs(uripath string, port int) error {
	if port < 1 || port > 65534 {
		return fmt.Errorf("Invalid port: %v", port)
	}

	http.HandleFunc(uripath, s.serveWs)

	return http.ListenAndServe(fmt.Sprintf(":%v", port), nil)
}

// RegisterWsHandler allows for registering of a websocket endpoint without having
// to start an http listener. For use when there is already an http listener active
// or the desire is to have multiple websocket endpoints.
func (s *Server) RegisterWsHandler(uripath string) {
	http.HandleFunc(uripath, s.serveWs)
}

func (s *Server) serveWs(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", 405)
		return
	}

	ws, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}

	c := &Connection{
		Conn:  *ws,
		Send:  make(chan *Message, s.wsBufSize),
		Recv:  make(chan *Message, s.wsBufSize),
		Error: make(chan error),
	}

	s.NewConn <- c

	go c.writePump()
	c.readPump(s.maxBytes)
}

func checkOriginStub(r *http.Request) bool {
	return true
}

func (c *Connection) readPump(maxBytes int64) {
	defer c.Close()

	c.SetReadLimit(maxBytes)
	c.SetReadDeadline(time.Now().Add(pongTimeout))
	c.SetPongHandler(func(string) error { c.SetReadDeadline(time.Now().Add(pongTimeout)); return nil })

	for {
		t, b, err := c.ReadMessage()

		if err != nil {
			break
		}

		msg := &Message{Type: t, Bytes: b}

		select {
		case c.Recv <- msg:

		case <-c.End:
			return

		}
	}
}

func (c *Connection) write(msg *Message) error {
	c.SetWriteDeadline(time.Now().Add(writeTimeout))
	return c.WriteMessage(msg.Type, msg.Bytes)
}

func (c *Connection) writePump() {
	ticker := time.NewTicker(pingPeriod)

	defer func() {
		ticker.Stop()
		c.Close()
	}()

	for {
		select {
		case message := <-c.Send:
			if err := c.write(message); err != nil {
				c.Error <- err
				return
			}

		case <-c.End:
			c.write(&Message{Type: websocket.CloseMessage, Bytes: []byte{}})
			return

		case <-ticker.C:
			if err := c.write(&Message{Type: websocket.PingMessage, Bytes: []byte{}}); err != nil {
				return
			}

		}
	}
}
