package handle

import (
	"fmt"
	"strconv"

	"github.com/kralamoure/d1/filter"
	"github.com/kralamoure/d1proto/msgcli"
	"github.com/kralamoure/d1proto/msgsvr"

	"github.com/kralamoure/d1game"
)

func AccountSetCharacter(svr *d1game.Server, sess *d1game.Session, msg msgcli.AccountSetCharacter) error {
	character, err := svr.Game.Character(filter.CharacterAccountIdEQ(sess.AccountId), filter.CharacterIdEQ(msg.Id))
	if err != nil {
		return err
	}
	sess.Character = character

	gfxIdStr := fmt.Sprintf("%d%d", character.Class, character.Gender)
	gfxId, err := strconv.ParseInt(gfxIdStr, 10, 32)
	if err != nil {
		return err
	}

	svr.SendPacketMsg(sess.Conn, &msgsvr.AccountCharacterSelectedSuccess{
		Id:     character.Id,
		Name:   string(character.Name),
		Level:  character.Level(),
		Guild:  0,
		Sex:    int(character.Gender),
		GFXId:  int(gfxId),
		Color1: string(character.Color1),
		Color2: string(character.Color2),
		Color3: string(character.Color3),
		Items:  "",
	})

	return nil
}
