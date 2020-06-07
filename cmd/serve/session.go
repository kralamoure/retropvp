package main

import (
	"bufio"
	"context"
	"net"
	"strings"
	"sync"

	"github.com/kralamoure/d1proto/msgsvr"

	"github.com/kralamoure/d1game"
)

func handleSession(svr *d1game.Server, ctx context.Context, conn *net.TCPConn) error {
	ctx, cancelCtx := context.WithCancel(ctx)
	defer cancelCtx()

	sess, err := d1game.NewSession(conn, cancelCtx)
	if err != nil {
		return err
	}

	svr.Log.Debugw("new session",
		"address", sess.Conn.RemoteAddr(),
	)
	svr.AddSession(sess)
	defer func() {
		sess.Conn.Close()
		svr.Log.Debugw("closed session connection",
			"address", sess.Conn.RemoteAddr(),
		)
		svr.DeleteSession(sess.Conn.RemoteAddr().String())
	}()

	ch := make(chan error, 1)

	wg := &sync.WaitGroup{}

	r := bufio.NewReader(sess.Conn)
	go func() {
		for {
			data, err := r.ReadString('\x00')
			if err != nil {
				ch <- err
				return
			}
			data = strings.Trim(data, " \t\r\n\x00")
			if data == "" {
				continue
			}

			wg.Add(1)
			err = handlePacketData(svr, sess, data)
			wg.Done()
			if err != nil {
				ch <- err
				return
			}
		}
	}()

	wg.Add(1)
	go func() {
		svr.HandlePacketsQueue(ctx, sess.Conn, sess.PktCh)
		wg.Done()
	}()

	svr.SendPacketMsg(sess.Conn, &msgsvr.AksHelloGame{})

	var e error
	select {
	case <-ctx.Done():
		e = ctx.Err()
	case err := <-ch:
		e = err
		cancelCtx()
	}

	wg.Wait()

	return e
}
