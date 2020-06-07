package d1game

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/kralamoure/d1"
	"github.com/kralamoure/d1proto/msgcli"
)

const (
	SessionStatusExpectingSendTicket SessionStatus = iota
	SessionStatusIdle
)

type SessionStatus int

type Session struct {
	Conn       *net.TCPConn
	Salt       string
	PktCh      chan QueuePacket
	Version    msgcli.AccountVersion
	Credential msgcli.AccountCredential
	AccountId  int
	LastAccess time.Time
	LastIP     string
	Character  d1.Character

	cancelFunc context.CancelFunc
	mu         *sync.Mutex
	status     SessionStatus
}

func NewSession(conn *net.TCPConn, cancelFunc context.CancelFunc) (*Session, error) {
	salt, err := RandomSalt(32)
	if err != nil {
		return nil, err
	}

	return &Session{
		Conn:       conn,
		PktCh:      make(chan QueuePacket),
		Salt:       salt,
		cancelFunc: cancelFunc,
		mu:         &sync.Mutex{},
	}, nil
}

func (s *Session) Status() SessionStatus {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.status
}

func (s *Session) SetStatus(status SessionStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.status = status
}

func (s *Session) CancelCtx() {
	s.cancelFunc()
}
