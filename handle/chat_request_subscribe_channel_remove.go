package handle

import (
	"github.com/kralamoure/d1/filter"
	"github.com/kralamoure/d1/typ"
	"github.com/kralamoure/d1proto/msgcli"
	"github.com/kralamoure/d1proto/msgsvr"

	"github.com/kralamoure/d1game"
)

func ChatRequestSubscribeChannelRemove(svr *d1game.Server, sess *d1game.Session, msg msgcli.ChatRequestSubscribeChannelRemove) error {
	chatChannels := make([]typ.ChatChannel, len(msg.Channels))
	for i := range msg.Channels {
		chatChannels[i] = typ.ChatChannel(msg.Channels[i])
	}

	account, err := svr.Login.Account(filter.AccountIdEQ(sess.AccountId))
	if err != nil {
		return err
	}

	err = svr.Login.RemoveUserChatChannels(account.UserId, chatChannels...)
	if err != nil {
		return err
	}

	svr.SendPacketMsg(sess.Conn, &msgsvr.ChatSubscribeChannelRemove{Channels: msg.Channels})

	return nil
}
