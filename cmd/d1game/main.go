package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"runtime/trace"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v4/pgxpool"
	"github.com/kralamoure/d1/d1svc"
	"github.com/kralamoure/d1pg"
	"github.com/kralamoure/dofus/dofussvc"
	"github.com/kralamoure/dofuspg"
	"github.com/spf13/pflag"
	"go.uber.org/zap"
	"go.uber.org/zap/buffer"

	"github.com/kralamoure/d1game"
)

const (
	programName        = "d1game"
	programDescription = "d1game is a game server for Dofus 1."
	programMoreInfo    = "https://github.com/kralamoure/d1game"
)

var (
	printHelp    bool
	debug        bool
	serverId     int
	serverAddr   string
	connTimeout  time.Duration
	pgConnString string

	serverSystemMarketId string
)

var (
	flagSet *pflag.FlagSet
	logger  *zap.SugaredLogger
)

func main() {
	l := log.New(os.Stderr, "", 0)

	initFlagSet()
	err := flagSet.Parse(os.Args)
	if err != nil {
		l.Println(err)
		os.Exit(2)
	}

	if printHelp {
		fmt.Println(help(flagSet.FlagUsages()))
		return
	}

	if debug {
		tmp, err := zap.NewDevelopment()
		if err != nil {
			l.Println(err)
			os.Exit(1)
		}
		logger = tmp.Sugar()
	} else {
		tmp, err := zap.NewProduction()
		if err != nil {
			l.Println(err)
			os.Exit(1)
		}
		logger = tmp.Sugar()
	}

	rand.Seed(time.Now().UnixNano())

	err = run()
	if err != nil {
		logger.Error(err)
		os.Exit(1)
	}
}

func run() error {
	defer logger.Sync()

	if debug {
		traceFile, err := os.Create("trace.out")
		if err != nil {
			return err
		}
		defer traceFile.Close()
		err = trace.Start(traceFile)
		if err != nil {
			return err
		}
		defer trace.Stop()
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	errCh := make(chan error)

	cfg, err := pgxpool.ParseConfig(pgConnString)
	if err != nil {
		return err
	}
	pool, err := pgxpool.ConnectConfig(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()

	dofusRepo, err := dofuspg.NewRepo(pool)
	if err != nil {
		return err
	}

	d1Repo, err := d1pg.NewRepo(pool)
	if err != nil {
		return err
	}

	dofusSvc, err := dofussvc.NewService(dofussvc.Config{
		Repo:   dofusRepo,
		Logger: logger.Named("dofussvc"),
	})
	if err != nil {
		return err
	}

	d1Svc, err := d1svc.NewService(d1svc.Config{
		GameServerId: serverId,
		Repo:         d1Repo,
		Logger:       logger.Named("d1svc"),
	})
	if err != nil {
		return err
	}

	svr, err := d1game.NewServer(d1game.Config{
		Id:          serverId,
		Addr:        serverAddr,
		ConnTimeout: connTimeout,
		Dofus:       dofusSvc,
		D1:          d1Svc,
		Logger:      logger.Named("server"),
	})
	if err != nil {
		return err
	}

	var wg sync.WaitGroup
	defer wg.Wait()

	wg.Add(1)
	go func() {
		defer wg.Done()
		err := svr.ListenAndServe(ctx)
		if err != nil {
			select {
			case errCh <- fmt.Errorf("error while listening and serving: %w", err):
			case <-ctx.Done():
			}
		}
	}()

	var selErr error
	select {
	case err := <-errCh:
		selErr = err
	case <-ctx.Done():
	}
	cancel()
	return selErr
}

func help(flagUsages string) string {
	buf := &buffer.Buffer{}
	fmt.Fprintf(buf, "%s\n\n", programDescription)
	fmt.Fprintf(buf, "Find more information at: %s\n\n", programMoreInfo)
	fmt.Fprint(buf, "Options:\n")
	fmt.Fprintf(buf, "%s\n", flagUsages)
	fmt.Fprintf(buf, "Usage: %s [options]", programName)
	return buf.String()
}

func initFlagSet() {
	flagSet = pflag.NewFlagSet("d1game", pflag.ContinueOnError)
	flagSet.BoolVarP(&printHelp, "help", "h", false, "Print usage information")
	flagSet.BoolVarP(&debug, "debug", "d", false, "Enable debug mode")
	flagSet.IntVarP(&serverId, "id", "i", 0, "Server ID")
	flagSet.StringVarP(&serverAddr, "address", "a", "0.0.0.0:5555", "Server listener address")
	flagSet.StringVarP(&pgConnString, "postgres", "p", "postgresql://user:password@host/database", "PostgreSQL connection string")
	flagSet.DurationVarP(&connTimeout, "timeout", "t", 30*time.Minute, "Connection timeout")

	flagSet.StringVarP(&serverSystemMarketId, "market", "m", "", "System's market id")

	flagSet.SortFlags = false
}
