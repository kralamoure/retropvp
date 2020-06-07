package handle

import (
	"github.com/kralamoure/d1proto/msgcli"
	"github.com/kralamoure/d1proto/msgsvr"

	"github.com/kralamoure/d1game"
)

func AccountRequestRegionalVersion(svr *d1game.Server, sess *d1game.Session, msg msgcli.AccountRequestRegionalVersion) error {
	svr.SendPacketMsg(sess.Conn, &msgsvr.AccountRegionalVersion{Value: 0})

	return nil
}
