// Package game provides a Dofus 1 game server that uses a d1 game service.
package d1game

import (
	"sync"

	"github.com/kralamoure/d1/service/game"
	"github.com/kralamoure/d1/service/login"
	"github.com/o1egl/paseto"
	"go.uber.org/zap"
)

type Config struct {
	Id     int
	Login  login.Service
	Game   game.Service
	Logger *zap.SugaredLogger
	// SharedKey should be 32 bytes long
	SharedKey []byte
}

type Server struct {
	Id        int
	Log       *zap.SugaredLogger
	Login     login.Service
	Game      game.Service
	sharedKey []byte

	mu        *sync.Mutex
	hostsData string
	sessions  map[string]*Session
}

func NewServer(cfg Config) *Server {
	svr := &Server{
		Id:        cfg.Id,
		Log:       cfg.Logger,
		Login:     cfg.Login,
		Game:      cfg.Game,
		sharedKey: cfg.SharedKey,

		mu:       &sync.Mutex{},
		sessions: make(map[string]*Session),
	}
	return svr
}

func (svr *Server) HostsData() string {
	svr.mu.Lock()
	defer svr.mu.Unlock()

	return svr.hostsData
}

func (svr *Server) SetHostsData(hosts string) {
	svr.mu.Lock()
	defer svr.mu.Unlock()

	svr.hostsData = hosts
}

func (svr *Server) Sessions() map[string]*Session {
	svr.mu.Lock()
	defer svr.mu.Unlock()

	return svr.sessions
}

func (svr *Server) AddSession(s *Session) {
	svr.mu.Lock()
	defer svr.mu.Unlock()

	svr.sessions[s.Conn.RemoteAddr().String()] = s
}

func (svr *Server) DeleteSession(remoteAddr string) {
	svr.mu.Lock()
	defer svr.mu.Unlock()

	delete(svr.sessions, remoteAddr)
}

func (svr *Server) Token(data string) (paseto.JSONToken, error) {
	token := paseto.JSONToken{}

	err := paseto.NewV2().Decrypt(data, svr.sharedKey, &token, nil)
	if err != nil {
		return token, err
	}

	return token, nil
}
