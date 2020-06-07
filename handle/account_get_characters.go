package handle

import (
	"fmt"
	"strconv"

	"github.com/kralamoure/d1/filter"
	"github.com/kralamoure/d1proto/msgcli"
	"github.com/kralamoure/d1proto/msgsvr"
	"github.com/kralamoure/d1proto/typ"

	"github.com/kralamoure/d1game"
)

func AccountGetCharacters(svr *d1game.Server, sess *d1game.Session, msg msgcli.AccountGetCharacters) error {
	account, err := svr.Login.Account(filter.AccountIdEQ(sess.AccountId))
	if err != nil {
		return err
	}

	allCharacters, err := svr.Login.Characters(filter.CharacterAccountIdEQ(sess.AccountId))
	if err != nil {
		return err
	}

	characters, err := svr.Game.Characters(filter.CharacterAccountIdEQ(sess.AccountId))
	if err != nil {
		return err
	}

	var chars []typ.AccountCharactersListCharacter
	for _, character := range characters {
		gfxIdStr := fmt.Sprintf("%d%d", character.Class, character.Gender)
		gfxId, err := strconv.ParseInt(gfxIdStr, 10, 32)
		if err != nil {
			return err
		}

		chars = append(chars, typ.AccountCharactersListCharacter{
			Id:          character.Id,
			Name:        string(character.Name),
			Level:       character.Level(),
			GFXId:       int(gfxId),
			Color1:      string(character.Color1),
			Color2:      string(character.Color2),
			Color3:      string(character.Color3),
			Accessories: typ.CommonAccessories{},
			Merchant:    false,
			ServerId:    character.GameServerId,
			Dead:        false,
			DeathCount:  0,
			LvlMax:      0,
		})
	}

	svr.SendPacketMsg(sess.Conn, &msgsvr.AccountCharactersListSuccess{
		Subscription:    account.SubscribedUntil,
		CharactersCount: len(allCharacters),
		Characters:      chars,
	})

	return nil
}
