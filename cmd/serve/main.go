package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/kralamoure/d1/repository/postgres"
	game2 "github.com/kralamoure/d1/service/game"
	"github.com/kralamoure/d1/service/login"
	"github.com/kralamoure/d1/typ"
	"go.uber.org/zap"

	"github.com/kralamoure/d1game"
)

type contextKey string

const (
	contextKeyName = contextKey("name")
)

var logger *zap.SugaredLogger

type config struct {
	id                 int
	host               string
	port               string
	postgresConnString string
	sharedKey          string
	dev                bool
}

func main() {
	cfg := config{}

	flag.IntVar(&cfg.id, "id", 0, "id of the server")
	flag.StringVar(&cfg.host, "host", "0.0.0.0", "host of the listener")
	flag.StringVar(&cfg.port, "port", "5555", "port of the listener")
	flag.StringVar(&cfg.postgresConnString, "db", "postgresql://user:password@host/database",
		"postgres connection string, either in URL or DSN format")
	flag.StringVar(&cfg.sharedKey, "key", "", "32 characters long shared key")
	flag.BoolVar(&cfg.dev, "dev", false, "enables development mode")

	flag.Parse()

	var tmpLogger *zap.Logger
	if cfg.dev {
		tmp, err := zap.NewDevelopment()
		if err != nil {
			log.Panicln(err)
		}
		tmpLogger = tmp
	} else {
		tmp, err := zap.NewProduction()
		if err != nil {
			log.Panicln(err)
		}
		tmpLogger = tmp
	}
	logger = tmpLogger.Sugar()
	defer logger.Sync()

	logger.Infof("initiating game server %d", cfg.id)

	ctx, cancelCtx := context.WithCancel(context.WithValue(context.Background(), contextKeyName, "main"))
	ctxName, ok := contextName(ctx)
	if ok {
		logger.Debugf("initiated context: %s", ctxName)
	}
	defer cancelCtx()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		logger.Debugf("received signal: %s", <-sigCh)
		signal.Stop(sigCh)
		cancelCtx()
	}()

	logger.Info("connecting to postgres")
	db, err := postgres.NewDB(context.Background(), cfg.postgresConnString)
	if err != nil {
		logger.Error(err)
		return
	}
	defer db.Close()

	svcCfg := d1game.Config{
		Id:        cfg.id,
		Login:     login.NewService(db),
		Game:      game2.NewService(db, cfg.id),
		Logger:    logger,
		SharedKey: []byte(cfg.sharedKey),
	}

	svr := d1game.NewServer(svcCfg)

	svr.Game.ChangeGameServerState(typ.GameServerStateOffline)
	defer svr.Game.ChangeGameServerState(typ.GameServerStateOffline)

	laddr, err := net.ResolveTCPAddr("tcp", net.JoinHostPort(cfg.host, cfg.port))
	if err != nil {
		logger.Errorf("could not resolve TCP address: %s", err)
		return
	}
	var ln *net.TCPListener
	if tmp, err := net.ListenTCP("tcp", laddr); err != nil {
		logger.Errorf("error while listening for connections: %s", err)
		return
	} else {
		ln = tmp
	}
	logger.Infof("listening for connections on %s", ln.Addr())
	defer ln.Close()

	connCh := make(chan *net.TCPConn, 0)
	go func() {
		defer close(connCh)
		for {
			conn, err := ln.AcceptTCP()
			if err != nil {
				if ctx.Err() == nil {
					logger.Debugf("error while accepting connection: %s", err)
					cancelCtx()
				}
				break
			}
			connCh <- conn
		}
	}()

	wg := &sync.WaitGroup{}

	svr.Game.ChangeGameServerState(typ.GameServerStateOnline)

LOOP:
	for {
		select {
		case <-ctx.Done():
			svr.Game.ChangeGameServerState(typ.GameServerStateStarting)
			name, _ := contextName(ctx)
			logger.Debugf("%s: %s", ctx.Err(), name)
			ln.Close()
			break LOOP
		case conn, more := <-connCh:
			if !more {
				continue
			}
			wg.Add(1)
			go func() {
				err := handleSession(svr, ctx, conn)
				if err != nil {
					if !errors.Is(err, context.Canceled) && !errors.Is(err, io.EOF) {
						logger.Debugw(fmt.Sprintf("error while handling session: %s", err.Error()),
							"address", conn.RemoteAddr(),
						)
					}
				}
				wg.Done()
			}()
		}
	}

	wg.Wait()
}

func contextName(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(contextKeyName).(string)
	return v, ok
}
