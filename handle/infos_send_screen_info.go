package handle

import (
	"github.com/kralamoure/d1proto/msgcli"
	"github.com/kralamoure/d1proto/msgsvr"

	"github.com/kralamoure/d1game"
)

func InfosSendScreenInfo(svr *d1game.Server, sess *d1game.Session, msg msgcli.InfosSendScreenInfo) error {
	svr.SendPacketMsg(sess.Conn, &msgsvr.BasicsNoticed{})

	return nil
}
