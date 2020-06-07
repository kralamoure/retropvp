package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/kralamoure/d1proto"
	"github.com/kralamoure/d1proto/msgcli"
	"github.com/kralamoure/d1proto/msgsvr"

	"github.com/kralamoure/d1game"
	"github.com/kralamoure/d1game/handle"
)

func handlePacketData(svr *d1game.Server, sess *d1game.Session, data string) error {
	var id d1proto.MsgCliId

	id, ok := d1proto.MsgCliIdByPkt(data)
	if !ok {
		return fmt.Errorf("unknown packet: %q", data)
	}

	extra := strings.TrimPrefix(data, string(id))

	name, ok := d1proto.MsgCliNameByID(id)
	if !ok {
		name = "Unknown"
	}

	svr.Log.Debugw("received packet",
		"name", name,
		"data", data,
		"address", sess.Conn.RemoteAddr().String(),
	)

	if !checkFrame(sess.Status(), id) {
		return errors.New("invalid frame")
	}

	switch id {
	case d1proto.AccountSendTicket:
		msg := msgcli.AccountSendTicket{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = handle.AccountSendTicket(svr, sess, msg)
		if err != nil {
			return err
		}
	case d1proto.AccountUseKey:
		msg := msgcli.AccountUseKey{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = handle.AccountUseKey(svr, sess, msg)
		if err != nil {
			return err
		}
	case d1proto.AccountRequestRegionalVersion:
		msg := msgcli.AccountRequestRegionalVersion{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = handle.AccountRequestRegionalVersion(svr, sess, msg)
		if err != nil {
			return err
		}
	case d1proto.AccountGetGifts:
		msg := msgcli.AccountGetGifts{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = handle.AccountGetGifts(svr, sess, msg)
		if err != nil {
			return err
		}
	case d1proto.AccountSendIdentity:
		msg := msgcli.AccountSendIdentity{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = handle.AccountSendIdentity(svr, sess, msg)
		if err != nil {
			return err
		}
	case d1proto.AccountQueuePosition:
		msg := msgcli.AccountQueuePosition{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = handle.AccountQueuePosition(svr, sess, msg)
		if err != nil {
			return err
		}
	case d1proto.AccountGetCharacters:
		msg := msgcli.AccountGetCharacters{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = handle.AccountGetCharacters(svr, sess, msg)
		if err != nil {
			return err
		}
	case d1proto.AccountGetCharactersForced:
		msg := msgcli.AccountGetCharactersForced{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = handle.AccountGetCharactersForced(svr, sess, msg)
		if err != nil {
			return err
		}
	case d1proto.AccountSetCharacter:
		msg := msgcli.AccountSetCharacter{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = handle.AccountSetCharacter(svr, sess, msg)
		if err != nil {
			return err
		}
	case d1proto.GameCreate:
		msg := msgcli.GameCreate{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = handle.GameCreate(svr, sess, msg)
		if err != nil {
			return err
		}
	case d1proto.AksPing:
		msg := msgcli.AksPing{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = handle.AksPing(svr, sess, msg)
		if err != nil {
			return err
		}
	case d1proto.BasicsGetDate:
		msg := msgcli.BasicsGetDate{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = handle.BasicsGetDate(svr, sess, msg)
		if err != nil {
			return err
		}
	case d1proto.InfosSendScreenInfo:
		msg := msgcli.InfosSendScreenInfo{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = handle.InfosSendScreenInfo(svr, sess, msg)
		if err != nil {
			return err
		}
	case d1proto.ChatRequestSubscribeChannelRemove:
		msg := msgcli.ChatRequestSubscribeChannelRemove{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = handle.ChatRequestSubscribeChannelRemove(svr, sess, msg)
		if err != nil {
			return err
		}
	case d1proto.ChatRequestSubscribeChannelAdd:
		msg := msgcli.ChatRequestSubscribeChannelAdd{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = handle.ChatRequestSubscribeChannelAdd(svr, sess, msg)
		if err != nil {
			return err
		}
	default:
		svr.Log.Debugw("unhandled packet",
			"name", name,
			"address", sess.Conn.RemoteAddr().String(),
		)
		svr.SendPacketMsg(sess.Conn, &msgsvr.BasicsNoticed{})
	}

	return nil
}

func checkFrame(status d1game.SessionStatus, msgId d1proto.MsgCliId) bool {
	switch status {
	case d1game.SessionStatusExpectingSendTicket:
		if msgId != d1proto.AccountSendTicket {
			return false
		}
	default:
		switch msgId {
		case d1proto.AccountSendTicket:
			return false
		}
	}

	return true
}
