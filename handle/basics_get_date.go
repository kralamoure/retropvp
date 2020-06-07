package handle

import (
	"time"

	"github.com/kralamoure/d1proto/msgcli"
	"github.com/kralamoure/d1proto/msgsvr"

	"github.com/kralamoure/d1game"
)

func BasicsGetDate(svr *d1game.Server, sess *d1game.Session, msg msgcli.BasicsGetDate) error {
	svr.SendPacketMsg(sess.Conn, &msgsvr.BasicsDate{Value: time.Now()})

	return nil
}
