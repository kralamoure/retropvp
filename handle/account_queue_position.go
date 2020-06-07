package handle

import (
	"github.com/kralamoure/d1proto/msgcli"
	"github.com/kralamoure/d1proto/msgsvr"

	"github.com/kralamoure/d1game"
)

func AccountQueuePosition(svr *d1game.Server, sess *d1game.Session, msg msgcli.AccountQueuePosition) error {
	svr.SendPacketMsg(sess.Conn, &msgsvr.AccountQueue{Position: 1})

	return nil
}
