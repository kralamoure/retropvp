package handle

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/kralamoure/d1proto/msgcli"
	"github.com/kralamoure/d1proto/msgsvr"

	"github.com/kralamoure/d1game"
)

func AccountSendTicket(svr *d1game.Server, sess *d1game.Session, msg msgcli.AccountSendTicket) error {
	token, err := svr.Token(msg.Id)
	if err != nil {
		return err
	}

	err = token.Validate()
	if err != nil {
		svr.SendPacketMsg(sess.Conn, &msgsvr.AccountTicketResponseError{})
		return err
	}

	if token.Get("serverId") != fmt.Sprintf("%d", svr.Id) {
		svr.SendPacketMsg(sess.Conn, &msgsvr.AccountTicketResponseError{})
		return errors.New("token server id is different")
	}

	accountId, err := strconv.Atoi(token.Subject)
	if err != nil {
		return err
	}
	sess.AccountId = accountId

	lastAccess, err := strconv.ParseInt(token.Get("lastAccess"), 10, 64)
	if err != nil {
		return err
	}

	sess.LastAccess = time.Unix(lastAccess, 0)
	sess.LastIP = token.Get("lastIP")

	sess.SetStatus(d1game.SessionStatusIdle)

	svr.SendPacketMsg(sess.Conn, &msgsvr.AccountTicketResponseSuccess{KeyId: 0})

	return nil
}
