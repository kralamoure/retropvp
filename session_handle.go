package retropvp

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/go-ozzo/ozzo-validation/v4/is"
	"github.com/kralamoure/dofus/dofustyp"
	"github.com/kralamoure/retro"
	"github.com/kralamoure/retro/retrotyp"
	protoenum "github.com/kralamoure/retroproto/enum"
	"github.com/kralamoure/retroproto/msgcli"
	"github.com/kralamoure/retroproto/msgsvr"
	prototyp "github.com/kralamoure/retroproto/typ"
	petname "github.com/zippoxer/golang-petname"
	"github.com/zippoxer/golang-petname/dict/large"
)

// TODO: sanitize user input

func (s *session) handleAccountQueuePosition() error {
	s.sendMessage(msgsvr.AccountQueue{Position: 1})
	return nil
}

func (s *session) handleAksPing() error {
	s.sendMessage(msgsvr.AksPong{})
	return nil
}

func (s *session) handleAksQuickPing() error {
	s.sendMessage(msgsvr.AksQuickPong{})
	return nil
}

func (s *session) handleBasicsRequestAveragePing() error {
	s.sendMessage(msgsvr.BasicsAveragePing{})
	return nil
}

func (s *session) handleBasicsGetDate() error {
	localizedTime := time.Now().In(s.svr.location)
	s.sendMessage(msgsvr.BasicsDate{
		Year:  localizedTime.Year(),
		Month: int(localizedTime.Month()),
		Day:   localizedTime.Day(),
	})
	return nil
}

func (s *session) handleInfosSendScreenInfo(ctx context.Context, m msgcli.InfosSendScreenInfo) error {
	s.sendMessage(msgsvr.BasicsNothing{})
	return nil
}

func (s *session) handleAccountSendTicket(ctx context.Context, m msgcli.AccountSendTicket) error {
	t, err := s.svr.retro.UseTicket(ctx, m.Ticket)
	if err != nil {
		return err
	}

	if t.GameServerId != s.svr.id {
		s.svr.logger.Debugw("different game server id",
			"client_address", s.conn.RemoteAddr().String(),
		)
		s.sendMessage(msgsvr.AccountTicketResponseError{})
		return errInvalidRequest
	}

	if t.Created.Add(s.svr.ticketDur).Before(time.Now().UTC()) {
		s.svr.logger.Debugw("ticket is expired",
			"client_address", s.conn.RemoteAddr().String(),
		)
		s.sendMessage(msgsvr.AccountTicketResponseError{})
		return errInvalidRequest
	}

	err = s.svr.controlAccount(t.AccountId, s)
	if err != nil {
		s.svr.logger.Debugw("could not control account",
			"error", err,
			"client_address", s.conn.RemoteAddr().String(),
		)
		s.sendMessage(msgsvr.AccountLoginError{
			Reason: protoenum.AccountLoginErrorReason.AlreadyLoggedGameServer,
		})
		return errInvalidRequest
	}

	account, err := s.svr.dofus.Account(ctx, s.accountId)
	if err != nil {
		return err
	}

	user, err := s.svr.dofus.User(ctx, account.UserId)
	if err != nil {
		return err
	}
	s.userId = user.Id

	ip, _, err := net.SplitHostPort(s.conn.RemoteAddr().String())
	if err != nil {
		return err
	}
	err = s.svr.dofus.SetAccountLastAccessAndLastIP(ctx, s.accountId, time.Now(), ip)
	if err != nil {
		return err
	}

	s.sendMessage(msgsvr.AccountTicketResponseSuccess{KeyId: 0})

	s.status.Store(statusExpectingAccountUseKey)
	return nil
}

func (s *session) handleAccountUseKey(m msgcli.AccountUseKey) error {
	if m.Id != 0 {
		s.svr.logger.Debugw("unexpected key id",
			"key_id", m.Id,
			"client_address", s.conn.RemoteAddr().String(),
		)
		return errInvalidRequest
	}
	s.sendMessage(msgsvr.BasicsNothing{})
	s.status.Store(statusExpectingAccountRequestRegionalVersion)
	return nil
}

func (s *session) handleAccountRequestRegionalVersion() error {
	s.sendMessage(msgsvr.AccountRegionalVersion{Value: 0})
	s.status.Store(statusExpectingAccountGetGifts)
	return nil
}

func (s *session) handleAccountGetGifts() error {
	s.sendMessage(msgsvr.BasicsNothing{})
	s.status.Store(statusExpectingAccountSetCharacter)
	return nil
}

func (s *session) handleAccountSendIdentity(ctx context.Context, m msgcli.AccountSendIdentity) error {
	s.sendMessage(msgsvr.BasicsNothing{})
	return nil
}

func (s *session) handleAccountGetCharacters(ctx context.Context) error {
	allChars, err := s.svr.retro.AllCharactersByAccountId(ctx, s.accountId)
	if err != nil {
		return err
	}
	chars := make(map[int]retro.Character)
	for id, accountChar := range allChars {
		if accountChar.GameServerId == s.svr.id {
			chars[id] = accountChar
		}
	}

	protoChars := make([]prototyp.AccountCharactersListCharacter, len(chars))
	var i int
	for _, char := range chars {
		gfxId, err := strconv.Atoi(fmt.Sprintf("%d%d", char.ClassId, char.Sex))
		if err != nil {
			return err
		}

		items, err := s.svr.retro.CharacterItemsByCharacterId(ctx, char.Id)
		if err != nil {
			return err
		}

		protoChars[i] = prototyp.AccountCharactersListCharacter{
			Id:          char.Id,
			Name:        string(char.Name),
			Level:       char.Level(),
			GFXId:       gfxId,
			Color1:      string(char.Color1),
			Color2:      string(char.Color2),
			Color3:      string(char.Color3),
			Accessories: protoAccessories(items),
			Merchant:    false,
			ServerId:    char.GameServerId,
			Dead:        false,
			DeathCount:  0,
			LvlMax:      0,
		}
		i++
	}

	account, err := s.svr.dofus.Account(ctx, s.accountId)
	if err != nil {
		return err
	}

	s.sendMessage(msgsvr.AccountCharactersListSuccess{
		Subscription:    account.Subscription,
		CharactersCount: len(allChars),
		Characters:      protoChars,
	})

	return nil
}

func (s *session) handleAccountGetCharactersForced(ctx context.Context) error {
	return s.handleAccountGetCharacters(ctx)
}

func (s *session) handleAccountGetRandomCharacterName() error {
	name := petname.Generate(large.Dict, 3, " ")
	sli := strings.Split(name, " ")
	if len(sli) != 3 {
		return errors.New("invalid name generation")
	}
	sli[0] = strings.Title(sli[0])
	name = strings.Join(sli[:2], "-")
	s.sendMessage(msgsvr.AccountCharacterNameGeneratedSuccess{Name: name})
	return nil
}

func (s *session) handleAccountAddCharacter(ctx context.Context, m msgcli.AccountAddCharacter) error {
	classId := retrotyp.ClassId(m.Class)
	err := validation.Validate(classId, validation.Required)
	if err != nil {
		return err
	}

	sex := retrotyp.Sex(m.Sex)
	err = validation.Validate(sex)
	if err != nil {
		return err
	}

	color1 := retrotyp.Color(m.Color1)
	err = validation.Validate(color1)
	if err != nil {
		return err
	}
	color2 := retrotyp.Color(m.Color2)
	err = validation.Validate(color2)
	if err != nil {
		return err
	}
	color3 := retrotyp.Color(m.Color3)
	err = validation.Validate(color3)
	if err != nil {
		return err
	}

	account, err := s.svr.dofus.Account(ctx, s.accountId)
	if err != nil {
		return err
	}

	if account.Subscription.Before(time.Now()) {
		s.sendMessage(msgsvr.AccountCharacterAddError{Reason: protoenum.AccountCharacterAddErrorReason.SubscriptionOut})
		return nil
	}

	accountCharacters, err := s.svr.retro.AllCharactersByAccountId(ctx, s.accountId)
	if err != nil {
		return err
	}

	if len(accountCharacters) >= 5 {
		s.sendMessage(msgsvr.AccountCharacterAddError{Reason: protoenum.AccountCharacterAddErrorReason.CreateCharacterFull})
		return nil
	}

	name := retrotyp.CharacterName(m.Name)
	err = validation.Validate(name, validation.Required)
	if err != nil {
		s.sendMessage(msgsvr.AccountCharacterAddError{Reason: protoenum.AccountCharacterAddErrorReason.CreateCharacterBadName})
		return nil
	}

	var specialClassSpellId int
	switch classId {
	case retrotyp.ClassIdFeca:
		specialClassSpellId = 422
	case retrotyp.ClassIdOsamodas:
		specialClassSpellId = 420
	case retrotyp.ClassIdEnutrof:
		specialClassSpellId = 425
	case retrotyp.ClassIdSram:
		specialClassSpellId = 416
	case retrotyp.ClassIdXelor:
		specialClassSpellId = 424
	case retrotyp.ClassIdEcaflip:
		specialClassSpellId = 412
	case retrotyp.ClassIdEniripsa:
		specialClassSpellId = 427
	case retrotyp.ClassIdIop:
		specialClassSpellId = 410
	case retrotyp.ClassIdCra:
		specialClassSpellId = 418
	case retrotyp.ClassIdSadida:
		specialClassSpellId = 426
	case retrotyp.ClassIdSacrier:
		specialClassSpellId = 421
	case retrotyp.ClassIdPandawa:
		specialClassSpellId = 423
	}
	specialClassSpell := retro.CharacterSpell{
		Id:       specialClassSpellId,
		Level:    5,
		Position: 4,
	}

	extraSpells := []retro.CharacterSpell{
		specialClassSpell,
		{
			Id:       370,
			Level:    5,
			Position: 5,
		},
		{
			Id:       373,
			Level:    5,
			Position: 6,
		},
		{
			Id:       391,
			Level:    5,
			Position: 7,
		},
		{
			Id:       368,
			Level:    5,
			Position: 8,
		},
		{
			Id:       350,
			Level:    5,
			Position: 9,
		},
		{
			Id:       369,
			Level:    5,
			Position: 10,
		},
		{
			Id:       366,
			Level:    5,
			Position: 11,
		},
		{
			Id:       364,
			Level:    5,
			Position: 12,
		},
		{
			Id:       367,
			Level:    5,
			Position: 13,
		},
		{
			Id:       394,
			Level:    5,
			Position: 14,
		},
		{
			Id:       390,
			Level:    5,
			Position: 0,
		},
		{
			Id:       392,
			Level:    5,
			Position: 0,
		},
		{
			Id:       393,
			Level:    5,
			Position: 0,
		},
		{
			Id:       395,
			Level:    5,
			Position: 0,
		},
		{
			Id:       396,
			Level:    5,
			Position: 0,
		},
		{
			Id:       397,
			Level:    5,
			Position: 0,
		},
	}

	class, ok := s.svr.cache.static.classes[classId]
	if !ok {
		return errors.New("class not found")
	}
	classSpellIds := class.Spells[:3]

	spells := make([]retro.CharacterSpell, len(extraSpells)+len(classSpellIds))
	copy(spells, extraSpells)
	for i, v := range classSpellIds {
		spells[i+len(extraSpells)] = retro.CharacterSpell{
			Id:       v,
			Level:    5,
			Position: i + 1,
		}
	}

	char := retro.Character{
		AccountId:    s.accountId,
		GameServerId: s.svr.id,
		Name:         name,
		Sex:          sex,
		ClassId:      classId,
		Color1:       color1,
		Color2:       color2,
		Color3:       color3,

		Stats: retro.CharacterStats{
			Vitality:     101,
			Wisdom:       101,
			Strength:     101,
			Intelligence: 101,
			Chance:       101,
			Agility:      101,
		},
		BonusPointsSpell: 1000,
		Kamas:            100,
		Direction:        1,
		GameMapId:        952,
		Cell:             100 + rand.Intn(101),
		Spells:           spells,
	}

	_, err = s.svr.retro.CreateCharacter(ctx, char)
	if err != nil {
		if errors.Is(err, retro.ErrCharacterNameAndGameServerIdAlreadyExist) {
			s.sendMessage(msgsvr.AccountCharacterAddError{Reason: protoenum.AccountCharacterAddErrorReason.NameAlreadyExists})
			return nil
		}
		return err
	}

	return s.handleAccountGetCharacters(ctx)
}

func (s *session) handleAccountDeleteCharacter(ctx context.Context, m msgcli.AccountDeleteCharacter) error {
	char, err := s.svr.retro.Character(ctx, m.Id)
	if err != nil {
		return err
	}
	if char.AccountId != s.accountId {
		s.svr.logger.Debugw("account does not own character",
			"error", err,
			"client_address", s.conn.RemoteAddr().String(),
		)
		return errInvalidRequest
	}

	if char.Level() >= 20 {
		user, err := s.svr.dofus.User(ctx, s.userId)
		if err != nil {
			return err
		}

		secretAnswer := user.SecretAnswer

		if !strings.EqualFold(strings.TrimSpace(m.SecretAnswer), strings.TrimSpace(secretAnswer)) {
			s.svr.logger.Debugw("wrong secret answer",
				"error", err,
				"client_address", s.conn.RemoteAddr().String(),
			)
			s.sendMessage(msgsvr.AccountCharacterDeleteError{})
			return nil
		}
	}

	err = s.svr.retro.DeleteCharacter(ctx, m.Id)
	if err != nil {
		return err
	}

	return s.handleAccountGetCharacters(ctx)
}

func (s *session) handleAccountSetCharacter(ctx context.Context, m msgcli.AccountSetCharacter) error {
	char, err := s.svr.retro.Character(ctx, m.Id)
	if err != nil {
		return err
	}
	if char.AccountId != s.accountId {
		s.svr.logger.Debugw("account does not own character",
			"error", err,
			"client_address", s.conn.RemoteAddr().String(),
		)
		return errInvalidRequest
	}

	s.svr.mu.Lock()
	s.characterId = char.Id
	s.svr.sessionByCharacterId[char.Id] = s
	s.svr.mu.Unlock()

	characterItems, err := s.svr.retro.CharacterItemsByCharacterId(ctx, char.Id)
	if err != nil {
		return err
	}

	gfxId, err := strconv.Atoi(fmt.Sprintf("%d%d", char.ClassId, char.Sex))
	if err != nil {
		return err
	}

	items := make([]prototyp.AccountCharacterSelectedSuccessItem, len(characterItems))
	i := 0
	for _, v := range characterItems {
		items[i] = prototyp.AccountCharacterSelectedSuccessItem{
			Id:         v.Id,
			TemplateId: v.TemplateId,
			Qty:        v.Quantity,
			Position:   v.Position,
			Effects:    v.DisplayEffects(),
		}
		i++
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Id < items[j].Id
	})

	s.sendMessage(msgsvr.AccountCharacterSelectedSuccess{
		Id:      char.Id,
		Name:    string(char.Name),
		Level:   char.Level(),
		ClassId: char.ClassId,
		Sex:     int(char.Sex),
		GFXId:   gfxId,
		Color1:  string(char.Color1),
		Color2:  string(char.Color2),
		Color3:  string(char.Color3),
		Items:   items,
	})

	s.status.Store(statusExpectingGameCreate)
	return nil
}

func (s *session) handleGameCreate(ctx context.Context, m msgcli.GameCreate) error {
	if m.Type != protoenum.GameCreateType.Solo {
		s.svr.logger.Debugw("wrong game create type",
			"type", m.Type,
			"client_address", s.conn.RemoteAddr().String(),
		)
		return errInvalidRequest
	}

	char, err := s.svr.retro.Character(ctx, s.characterId)
	if err != nil {
		return err
	}

	items, err := s.svr.retro.CharacterItemsByCharacterId(ctx, s.characterId)
	if err != nil {
		return err
	}

	itemSets := make(map[int]struct{})
	for _, v := range items {
		if v.Position == retrotyp.CharacterItemPositionInventory || v.Position >= 35 {
			continue
		}

		template, ok := s.svr.cache.static.items[v.TemplateId]
		if !ok {
			return errors.New("item template not found")
		}
		if template.ItemSetId != 0 {
			itemSets[template.ItemSetId] = struct{}{}
		}
	}
	for k := range itemSets {
		err := s.sendItemSetBonus(ctx, k)
		if err != nil {
			return err
		}
	}

	if char.MountId != 0 {
		mount, err := s.svr.retro.Mount(ctx, char.MountId)
		if err != nil {
			return err
		}

		mountData, err := s.svr.commonMountData(mount)
		if err != nil {
			return err
		}

		s.sendMessage(msgsvr.MountEquipSuccess{Data: mountData})
	}

	s.sendMessage(msgsvr.MountXP{Percent: 0})

	var specialization int
	switch char.Alignment {
	case retrotyp.AlignmentBontarian:
		specialization = 1
	case retrotyp.AlignmentBrakmarian:
		specialization = 18
	case retrotyp.AlignmentMercenary:
		specialization = 34
	}
	s.sendMessage(msgsvr.SpecializationSet{Value: specialization})

	user, err := s.svr.dofus.User(ctx, s.userId)
	if err != nil {
		return err
	}

	chatChannels := &strings.Builder{}
	if user.ChatChannels.Admin {
		chatChannels.WriteRune(rune(dofustyp.ChatChannelAdmin))
	}
	if user.ChatChannels.Info {
		chatChannels.WriteRune(rune(dofustyp.ChatChannelInfo))
	}
	if user.ChatChannels.Public {
		chatChannels.WriteRune(rune(dofustyp.ChatChannelPublic))
	}
	if user.ChatChannels.Private {
		chatChannels.WriteRune(rune(dofustyp.ChatChannelPrivate))
	}
	if user.ChatChannels.Group {
		chatChannels.WriteRune(rune(dofustyp.ChatChannelGroup))
	}
	if user.ChatChannels.Team {
		chatChannels.WriteRune(rune(dofustyp.ChatChannelTeam))
	}
	if user.ChatChannels.Guild {
		chatChannels.WriteRune(rune(dofustyp.ChatChannelGuild))
	}
	if user.ChatChannels.Alignment {
		chatChannels.WriteRune(rune(dofustyp.ChatChannelAlignment))
	}
	if user.ChatChannels.Recruitment {
		chatChannels.WriteRune(rune(dofustyp.ChatChannelRecruitment))
	}
	if user.ChatChannels.Trading {
		chatChannels.WriteRune(rune(dofustyp.ChatChannelTrading))
	}
	if user.ChatChannels.Newbies {
		chatChannels.WriteRune(rune(dofustyp.ChatChannelNewbies))
	}
	s.sendMessage(msgsvr.ChatSubscribeChannelAdd{Channels: []rune(chatChannels.String())})

	s.sendMessage(msgsvr.SpellsChangeOption{CanUseSeeAllSpell: true})

	s.sendMessage(msgsvr.SpellsList{Spells: char.Spells})

	s.sendMessage(msgsvr.AccountRestrictions{
		Restrictions: prototyp.CommonRestrictions{
			CantAssault:                          false,
			CantChallenge:                        false,
			CantExchange:                         false,
			CantAttack:                           true,
			CantChatToAll:                        false,
			CantBeMerchant:                       false,
			CantUseObject:                        false,
			CantInteractWithTaxCollector:         false,
			CantUseInteractiveObjects:            false,
			CantSpeakNPC:                         false,
			CantAttackDungeonMonstersWhenMutant:  true,
			CantMoveInAllDirections:              false,
			CantAttackMonstersAnywhereWhenMutant: true,
			CantInteractWithPrism:                false,
		},
	}) // TODO

	err = s.sendWeight(ctx)
	if err != nil {
		return err
	}

	s.sendMessage(msgsvr.FriendsNotifyChange{Notify: true}) // TODO

	s.sendMessage(msgsvr.InfosMessage{
		ChatId: protoenum.InfosMessageChatId.Error,
		Messages: []prototyp.InfosMessageMessage{
			{
				Id:   89,
				Args: nil,
			},
		},
	})

	account, err := s.svr.dofus.Account(ctx, s.accountId)
	if err != nil {
		return err
	}

	firstTime := account.LastAccess.IsZero()
	if !firstTime {
		localizedTime := account.LastAccess.In(s.svr.location)

		s.sendMessage(msgsvr.InfosMessage{
			ChatId: protoenum.InfosMessageChatId.Info,
			Messages: []prototyp.InfosMessageMessage{
				{
					Id: 152,
					Args: []string{
						fmt.Sprintf("%d", localizedTime.Year()),
						fmt.Sprintf("%d", localizedTime.Month()),
						fmt.Sprintf("%d", localizedTime.Day()),
						fmt.Sprintf("%d", localizedTime.Hour()),
						fmt.Sprintf("%02d", localizedTime.Minute()),
						account.LastIP,
					},
				},
			},
		})
	}

	ip, _, err := net.SplitHostPort(s.conn.RemoteAddr().String())
	if err != nil {
		return err
	}
	s.sendMessage(msgsvr.InfosMessage{
		ChatId: protoenum.InfosMessageChatId.Info,
		Messages: []prototyp.InfosMessageMessage{
			{
				Id:   153,
				Args: []string{ip},
			},
		},
	})

	s.sendMessage(msgsvr.GameCreateSuccess{Type: protoenum.GameCreateType.Solo})

	stats, err := s.protoStats(ctx)
	if err != nil {
		return err
	}
	s.sendMessage(stats)

	s.sendMessage(msgsvr.InfosLifeRestoreTimerStart{Interval: time.Second * 2}) // TODO

	gameMap, ok := s.svr.cache.static.gameMaps[char.GameMapId]
	if !ok {
		return errors.New("invalid game map")
	}

	s.sendMessage(msgsvr.GameMapData{
		Id:   gameMap.Id,
		Name: gameMap.Name,
		Key:  gameMap.Key,
	})

	s.sendMessage(msgsvr.BasicsTime{Value: time.Now()})

	s.sendMessage(msgsvr.FightsCount{Value: 0}) // TODO

	s.sendMessage(msgsvr.TutorialShowTip{Id: 32}) // TODO

	sprite, err := s.svr.gameMovementSpriteCharacter(ctx, char, false)
	if err != nil {
		return err
	}

	err = s.svr.sendMsgToMap(ctx, char.GameMapId, msgsvr.GameMovement{Sprites: []msgsvr.GameMovementSprite{sprite}})
	if err != nil {
		return err
	}

	s.status.Store(statusIdle)
	return nil
}

func (s *session) handleChatRequestSubscribeChannelAdd(ctx context.Context, m msgcli.ChatRequestSubscribeChannelAdd) error {
	if len(m.Channels) == 0 {
		return errInvalidRequest
	}

	chatChannels := make([]dofustyp.ChatChannel, len(m.Channels))
	for i, chatChannel := range m.Channels {
		if dofustyp.ChatChannel(chatChannel) == dofustyp.ChatChannelAdmin {
			return errInvalidRequest
		}
		_, ok := dofustyp.ChatChannels[dofustyp.ChatChannel(chatChannel)]
		if !ok {
			return errInvalidRequest
		}
		chatChannels[i] = dofustyp.ChatChannel(chatChannel)
	}

	err := s.svr.dofus.UserAddChatChannel(ctx, s.userId, chatChannels...)
	if err != nil {
		return err
	}

	s.sendMessage(msgsvr.ChatSubscribeChannelAdd{Channels: m.Channels})
	return nil
}

func (s *session) handleChatRequestSubscribeChannelRemove(ctx context.Context, m msgcli.ChatRequestSubscribeChannelRemove) error {
	if len(m.Channels) == 0 {
		return errInvalidRequest
	}

	chatChannels := make([]dofustyp.ChatChannel, len(m.Channels))
	for i, chatChannel := range m.Channels {
		if dofustyp.ChatChannel(chatChannel) == dofustyp.ChatChannelAdmin {
			return errInvalidRequest
		}
		_, ok := dofustyp.ChatChannels[dofustyp.ChatChannel(chatChannel)]
		if !ok {
			return errInvalidRequest
		}
		chatChannels[i] = dofustyp.ChatChannel(chatChannel)
	}

	err := s.svr.dofus.UserRemoveChatChannel(ctx, s.userId, chatChannels...)
	if err != nil {
		return err
	}

	s.sendMessage(msgsvr.ChatSubscribeChannelRemove{Channels: m.Channels})
	return nil
}

func (s *session) handleGameGetExtraInformations(ctx context.Context) error {
	char, err := s.svr.retro.Character(ctx, s.characterId)
	if err != nil {
		return err
	}

	npcs := s.svr.cache.npcsByMapId[char.GameMapId]
	npcSprites := make([]msgsvr.GameMovementSprite, len(npcs))
	for i, v := range npcs {
		npcSprites[i] = msgsvr.GameMovementSprite{
			Transition: false,
			Fight:      false,
			Type:       protoenum.GameMovementSpriteType.NPC,
			Id:         -(i + 1),
			CellId:     v.CellId,
			Direction:  v.Direction,
			NPC: msgsvr.GameMovementNPC{
				TemplateId:    v.TemplateId,
				GFXId:         v.GFX,
				Sex:           int(v.Sex),
				ScaleX:        v.ScaleX,
				ScaleY:        v.ScaleY,
				Color1:        string(v.Color1),
				Color2:        string(v.Color2),
				Color3:        string(v.Color3),
				Accessories:   prototyp.CommonAccessories{}, // TODO
				ExtraClipId:   v.ExtraClip,
				CustomArtwork: v.CustomArtwork,
			},
		}
	}

	s.svr.mu.Lock()
	chars, err := s.svr.retro.CharactersByGameMapId(ctx, char.GameMapId)
	if err != nil {
		s.svr.mu.Unlock()
		return err
	}

	for k := range chars {
		_, ok := s.svr.sessionByCharacterId[k]
		if !ok {
			delete(chars, k)
		}
	}
	s.svr.mu.Unlock()

	charSprites := make([]msgsvr.GameMovementSprite, len(chars))
	i := 0
	for _, v := range chars {
		sprite, err := s.svr.gameMovementSpriteCharacter(ctx, v, false)
		if err != nil {
			return err
		}
		charSprites[i] = sprite

		i++
	}

	sprites := append(npcSprites, charSprites...)

	s.sendMessage(msgsvr.GameMovement{Sprites: sprites})

	s.sendMessage(msgsvr.GameMapLoaded{})

	return nil
}

// TODO
func (s *session) handleChatSend(ctx context.Context, m msgcli.ChatSend) error {
	_, ok := dofustyp.ChatChannels[m.ChatChannel]
	if !ok {
		return errInvalidRequest
	}

	if m.Message == "" {
		s.sendMessage(msgsvr.BasicsNothing{})
		return nil
	}

	account, err := s.svr.dofus.Account(ctx, s.accountId)
	if err != nil {
		return err
	}

	char, err := s.svr.retro.Character(ctx, s.characterId)
	if err != nil {
		return err
	}

	switch m.ChatChannel {
	case dofustyp.ChatChannelAdmin:
		if !account.Admin {
			s.sendMessage(msgsvr.BasicsNothing{})
			return nil
		}

		s.sendMessage(msgsvr.ChatMessageSuccess{
			ChatChannel: m.ChatChannel,
			Id:          char.Id,
			Name:        string(char.Name),
			Message:     m.Message,
			Params:      m.Params,
		})
	case dofustyp.ChatChannelPublic:
		if account.Admin {
			if len(m.Message) >= 2 && m.Message[0] == '.' {
				return s.chatCommand(ctx, m.Message[1:])
			}
		}

		s.sendMessage(msgsvr.ChatMessageSuccess{
			ChatChannel: m.ChatChannel,
			Id:          char.Id,
			PrivateTo:   false,
			Name:        string(char.Name),
			Message:     m.Message,
			Params:      m.Params,
		})
	default:
		s.sendMessage(msgsvr.InfosMessage{
			ChatId: protoenum.InfosMessageChatId.Error,
			Messages: []prototyp.InfosMessageMessage{
				{
					Id:   16,
					Args: []string{"<b>Error</b>", "Not implemented."},
				},
			},
		})
		return nil
	}

	return nil
}

func (s *session) handleDialogCreate(ctx context.Context, m msgcli.DialogCreate) error {
	if m.NPCId >= 0 {
		return errInvalidRequest
	}

	i := m.NPCId*-1 - 1

	char, err := s.svr.retro.Character(ctx, s.characterId)
	if err != nil {
		return err
	}

	npcs := s.svr.cache.npcsByMapId[char.GameMapId]
	if len(npcs) <= i {
		return errInvalidRequest
	}

	npc := npcs[i]

	if npc.DialogId <= 0 {
		return errInvalidRequest
	}

	dialog, ok := s.svr.cache.static.npcDialogs[npc.DialogId]
	if !ok {
		return fmt.Errorf("dialog does not exist: %d", npc.DialogId)
	}

	s.sendMessage(msgsvr.DialogCreateSuccess{NPCId: m.NPCId})

	s.sendMessage(msgsvr.DialogQuestion{
		Question: dialog.Id,
		Answers:  dialog.Responses,
	})

	return nil
}

func (s *session) handleDialogRequestLeave(ctx context.Context) error {
	s.sendMessage(msgsvr.DialogLeave{})

	return nil
}

func (s *session) handleDialogResponse(ctx context.Context, m msgcli.DialogResponse) error {
	// TODO: check question is valid for current dialog context (and some other security checks too)

	response, ok := s.svr.cache.static.npcResponses[m.Answer]
	if !ok {
		return errInvalidRequest
	}

	switch response.Action {
	case retrotyp.NPCResponseActionLeaveDialog:
		s.sendMessage(msgsvr.DialogLeave{})
	case retrotyp.NPCResponseActionCreateDialog:
		if len(response.Arguments) == 0 {
			return errors.New("missing response argument")
		}
		dialogId, err := strconv.Atoi(response.Arguments[0])
		if err != nil {
			return err
		}
		dialog, ok := s.svr.cache.static.npcDialogs[dialogId]
		if !ok {
			return errors.New("invalid dialog id")
		}
		s.sendMessage(msgsvr.DialogQuestion{
			Question: dialog.Id,
			Answers:  dialog.Responses,
		})
	}

	return nil
}

func (s *session) handleExchangeRequest(ctx context.Context, m msgcli.ExchangeRequest) error {
	if m.Type != int(retrotyp.ExchangeNPCBuy) {
		return errNotImplemented
	}

	if m.Id >= 0 {
		return errInvalidRequest
	}

	char, err := s.svr.retro.Character(ctx, s.characterId)
	if err != nil {
		return err
	}

	i := m.Id*-1 - 1
	npcs := s.svr.cache.npcsByMapId[char.GameMapId]
	if len(npcs) <= i {
		return errInvalidRequest
	}

	npc := npcs[i]

	if npc.MarketId == "" {
		return errInvalidRequest
	}

	market, ok := s.svr.cache.markets[npc.MarketId]
	if !ok {
		return fmt.Errorf("market does not exist: %q", npc.MarketId)
	}
	s.cache.exchangeMarket = &market

	s.sendMessage(msgsvr.ExchangeCreateSuccess{
		Type: retrotyp.ExchangeNPCBuy,
		NPCBuy: msgsvr.ExchangeCreateSuccessNPCBuy{
			Quantity1:     market.Quantity1,
			Quantity2:     market.Quantity2,
			Quantity3:     market.Quantity3,
			Types:         market.Types,
			Fee:           market.Fee,
			MaxLevel:      market.MaxLevel,
			MaxPerAccount: market.MaxPerAccount,
			NPCId:         npc.TemplateId,
			MaxHours:      market.MaxHours,
		},
	})

	return nil
}

func (s *session) handleExchangeLeave(ctx context.Context) error {
	s.cache.exchangeMarket = nil
	s.sendMessage(msgsvr.ExchangeLeaveSuccess{})
	return nil
}

func (s *session) handleExchangeBigStoreType(ctx context.Context, m msgcli.ExchangeBigStoreType) error {
	if s.cache.exchangeMarket == nil {
		return errInvalidRequest
	}

	templateIds, err := s.svr.marketTemplateIdsByItemType(ctx, *s.cache.exchangeMarket, m.ItemType)
	if err != nil {
		return err
	}

	s.sendMessage(msgsvr.ExchangeBigStoreTypeItemsList{
		ItemType:        m.ItemType,
		ItemTemplateIds: templateIds,
	})

	return nil
}

func (s *session) handleExchangeBigStoreItemList(ctx context.Context, m msgcli.ExchangeBigStoreItemList) error {
	if s.cache.exchangeMarket == nil {
		return errInvalidRequest
	}

	items, err := s.svr.marketItemsByTemplateId(ctx, *s.cache.exchangeMarket, m.ItemTemplateId)
	if err != nil {
		return err
	}

	s.sendMessage(msgsvr.ExchangeBigStoreItemsList{
		TemplateId: m.ItemTemplateId,
		Items:      items,
	})

	return nil
}

func (s *session) handleExchangeBigStoreSearch(ctx context.Context, m msgcli.ExchangeBigStoreSearch) error {
	if s.cache.exchangeMarket == nil {
		return errInvalidRequest
	}

	templateIds, err := s.svr.marketTemplateIdsByItemType(ctx, *s.cache.exchangeMarket, m.ItemType)
	if err != nil {
		return err
	}

	items, err := s.svr.marketItemsByTemplateId(ctx, *s.cache.exchangeMarket, m.TemplateId)
	if err != nil {
		return err
	}

	if len(items) == 0 {
		s.sendMessage(msgsvr.ExchangeSearchError{})
		return nil
	}

	s.sendMessage(msgsvr.ExchangeSearchSuccess{})

	s.sendMessage(msgsvr.ExchangeBigStoreTypeItemsList{
		ItemType:        m.ItemType,
		ItemTemplateIds: templateIds,
	})

	s.sendMessage(msgsvr.ExchangeBigStoreItemsList{
		TemplateId: m.TemplateId,
		Items:      items,
	})

	return nil
}

func (s *session) handleExchangeGetItemMiddlePriceInBigStore(ctx context.Context, m msgcli.ExchangeGetItemMiddlePriceInBigStore) error {
	if s.cache.exchangeMarket == nil {
		return errInvalidRequest
	}

	s.sendMessage(msgsvr.ExchangeBigStoreItemMiddlePriceInBigStore{
		TemplateId: m.TemplateId,
		Price:      1,
	})

	return nil
}

func (s *session) handleExchangeBigStoreBuy(ctx context.Context, m msgcli.ExchangeBigStoreBuy) error {
	if s.cache.exchangeMarket == nil {
		return errInvalidRequest
	}

	items, ok := s.svr.cache.marketItemsByMarketId[s.cache.exchangeMarket.Id]
	if !ok {
		return errInvalidRequest
	}

	item, ok := items[m.ItemId]
	if !ok {
		return errInvalidRequest
	}

	itemTemplate, ok := s.svr.cache.static.items[item.TemplateId]
	if !ok {
		return errors.New("item template not found")
	}

	char, err := s.svr.retro.Character(ctx, s.characterId)
	if err != nil {
		return err
	}

	if itemTemplate.Type == retrotyp.ItemTypeMountCertificate {
		mountTemplateId, ok := retro.MountTemplateIdByMountCertificateId[itemTemplate.Id]
		if !ok {
			return errors.New("mount template id not found")
		}

		validity := time.Now().Add(time.Hour * 24 * 20).Truncate(time.Minute)

		mountId, err := s.svr.retro.CreateMount(ctx, retro.Mount{
			TemplateId:  mountTemplateId,
			CharacterId: 0,
			Name:        "",
			Sex:         retrotyp.Sex(rand.Intn(2)),
			XP:          retro.MountXPFloors[len(retro.MountXPFloors)-1],
			Capacities:  nil,
			Validity:    validity,
		})
		if err != nil {
			return err
		}

		item.Effects = []retrotyp.Effect{
			{
				Id:       995,
				DiceNum:  mountId,
				DiceSide: int(validity.Unix()) * 1000,
			},
		}
	}

	if item.Price > char.Kamas {
		s.sendMessage(msgsvr.ExchangeBuyError{})
		return nil
	}

	char.Kamas -= item.Price

	err = s.svr.retro.UpdateCharacter(ctx, char)
	if err != nil {
		return err
	}

	stats, err := s.protoStats(ctx)
	if err != nil {
		return err
	}
	s.sendMessage(stats)

	_, err = s.addItemToInventory(ctx, item.Item)
	if err != nil {
		return err
	}

	err = s.sendWeight(ctx)
	if err != nil {
		return err
	}

	s.sendMessage(msgsvr.ExchangeBuySuccess{})

	return nil
}

func (s *session) handleItemsDestroy(ctx context.Context, m msgcli.ItemsDestroy) error {
	if m.Quantity < 1 {
		return errInvalidRequest
	}

	item, err := s.svr.retro.CharacterItem(ctx, m.Id)
	if err != nil {
		return err
	}

	if item.CharacterId != s.characterId {
		return errInvalidRequest
	}

	if m.Quantity > item.Quantity {
		return errInvalidRequest
	}

	if (item.Position < retrotyp.CharacterItemPositionInventory || item.Position > retrotyp.CharacterItemPositionShield) &&
		(item.Position < 35 || item.Position > 62) {
		return errInvalidRequest
	}

	err = s.removeItem(ctx, item.Id, m.Quantity)
	if err != nil {
		return err
	}

	char, err := s.svr.retro.Character(ctx, s.characterId)
	if err != nil {
		return err
	}
	char.Kamas += m.Quantity

	err = s.svr.retro.UpdateCharacter(ctx, char)
	if err != nil {
		return err
	}

	err = s.sendWeight(ctx)
	if err != nil {
		return err
	}

	stats, err := s.protoStats(ctx)
	if err != nil {
		return err
	}
	s.sendMessage(stats)

	switch item.Position {
	case retrotyp.CharacterItemPositionWeapon,
		retrotyp.CharacterItemPositionHat,
		retrotyp.CharacterItemPositionCloak,
		retrotyp.CharacterItemPositionPet,
		retrotyp.CharacterItemPositionShield:

		err := s.sendAccessories(ctx)
		if err != nil {
			return err
		}
	}

	if item.Position >= retrotyp.CharacterItemPositionAmulet && item.Position <= retrotyp.CharacterItemPositionShield {
		err = s.checkConditions(ctx)
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *session) handleItemsDrop(ctx context.Context, m msgcli.ItemsDrop) error {
	return s.handleItemsDestroy(ctx, msgcli.ItemsDestroy(m))
}

// TODO
func (s *session) handleItemsRequestMovement(ctx context.Context, m msgcli.ItemsRequestMovement) error {
	if m.Quantity < 1 {
		return errInvalidRequest
	}

	item, err := s.svr.retro.CharacterItem(ctx, m.Id)
	if err != nil {
		return err
	}

	if item.CharacterId != s.characterId {
		return errInvalidRequest
	}

	if m.Position == item.Position {
		return errInvalidRequest
	}

	if m.Quantity > item.Quantity {
		return errInvalidRequest
	}

	if m.Position == retrotyp.CharacterItemPositionInventory {
		if item.Position >= retrotyp.CharacterItemPositionAmulet && item.Position <= retrotyp.CharacterItemPositionShield {
			err := s.unEquip(ctx, item.Id)
			if err != nil {
				return err
			}

			err = s.checkConditions(ctx)
			if err != nil {
				return err
			}
		} else if item.Position >= 35 && item.Position <= 62 {
			err := s.moveItemToPosition(ctx, item.Id, m.Quantity, m.Position)
			if err != nil {
				return err
			}
		} else {
			return errInvalidRequest
		}
	} else if m.Position >= retrotyp.CharacterItemPositionAmulet && m.Position <= retrotyp.CharacterItemPositionShield {
		err := s.equip(ctx, item.Id, m.Position)
		if err != nil {
			return err
		}

		err = s.checkConditions(ctx)
		if err != nil {
			return err
		}
	} else if m.Position >= 35 && m.Position <= 62 {
		if item.Position == retrotyp.CharacterItemPositionInventory {
			itemTemplate, ok := s.svr.cache.static.items[item.TemplateId]
			if !ok {
				return fmt.Errorf("invalid item template")
			}

			if !(itemTemplate.CanUse || itemTemplate.CanTarget) {
				return errInvalidRequest
			}

			if itemTemplate.Type == retrotyp.ItemTypeCandy {
				return errNotAllowed
			}

			err := s.moveItemToPosition(ctx, item.Id, m.Quantity, m.Position)
			if err != nil {
				return err
			}
		} else if item.Position >= 35 && item.Position <= 62 {
			err := s.moveItemToPosition(ctx, item.Id, m.Quantity, m.Position)
			if err != nil {
				return err
			}
		} else {
			return errInvalidRequest
		}
	} else {
		return errNotAllowed
	}

	err = s.sendWeight(ctx)
	if err != nil {
		return err
	}

	return nil
}

func (s *session) handleAccountBoost(ctx context.Context, m msgcli.AccountBoost) error {
	char, err := s.svr.retro.Character(ctx, s.characterId)
	if err != nil {
		return err
	}
	fmt.Println(char.Stats)

	class, ok := s.svr.cache.static.classes[char.ClassId]
	if !ok {
		return errors.New("class not found")
	}

	cost := 1
	bonus := 1
	switch m.CharacteristicId {
	case retrotyp.CharacteristicIdVitality:
		currentQty := char.Stats.Vitality
		for _, v := range class.BoostCosts.Vitality {
			if v.Quantity > currentQty {
				break
			}
			cost = v.Cost
			bonus = v.Bonus
		}
	case retrotyp.CharacteristicIdWisdom:
		currentQty := char.Stats.Wisdom
		for _, v := range class.BoostCosts.Wisdom {
			if v.Quantity > currentQty {
				break
			}
			cost = v.Cost
			bonus = v.Bonus
		}
	case retrotyp.CharacteristicIdStrength:
		currentQty := char.Stats.Strength
		for _, v := range class.BoostCosts.Strength {
			if v.Quantity > currentQty {
				break
			}
			cost = v.Cost
			bonus = v.Bonus
		}
	case retrotyp.CharacteristicIdIntelligence:
		currentQty := char.Stats.Intelligence
		for _, v := range class.BoostCosts.Intelligence {
			if v.Quantity > currentQty {
				break
			}
			cost = v.Cost
			bonus = v.Bonus
		}
	case retrotyp.CharacteristicIdChance:
		currentQty := char.Stats.Chance
		for _, v := range class.BoostCosts.Chance {
			if v.Quantity > currentQty {
				break
			}
			cost = v.Cost
			bonus = v.Bonus
		}
	case retrotyp.CharacteristicIdAgility:
		currentQty := char.Stats.Agility
		for _, v := range class.BoostCosts.Agility {
			if v.Quantity > currentQty {
				break
			}
			cost = v.Cost
			bonus = v.Bonus
		}
	default:
		return errors.New("characteristic id is invalid")
	}

	if char.BonusPoints < cost {
		return errors.New("bonus points are insufficient")
	}
	char.BonusPoints -= cost

	switch m.CharacteristicId {
	case retrotyp.CharacteristicIdVitality:
		char.Stats.Vitality += bonus
	case retrotyp.CharacteristicIdWisdom:
		char.Stats.Wisdom += bonus
	case retrotyp.CharacteristicIdStrength:
		char.Stats.Strength += bonus
	case retrotyp.CharacteristicIdIntelligence:
		char.Stats.Intelligence += bonus
	case retrotyp.CharacteristicIdChance:
		char.Stats.Chance += bonus
	case retrotyp.CharacteristicIdAgility:
		char.Stats.Agility += bonus
	}

	err = s.svr.retro.UpdateCharacter(ctx, char)
	if err != nil {
		return err
	}

	if m.CharacteristicId == retrotyp.CharacteristicIdStrength {
		err = s.sendWeight(ctx)
		if err != nil {
			return err
		}
	}

	stats, err := s.protoStats(ctx)
	if err != nil {
		return err
	}
	s.sendMessage(stats)

	err = s.checkConditions(ctx)
	if err != nil {
		return err
	}

	return nil
}

func (s *session) handleSpellsBoost(ctx context.Context, m msgcli.SpellsBoost) error {
	char, err := s.svr.retro.Character(ctx, s.characterId)
	if err != nil {
		return err
	}

	var level int

	found := false
	for i, spell := range char.Spells {
		if spell.Id != m.Id {
			continue
		}
		found = true

		t, ok := s.svr.cache.static.spells[spell.Id]
		if !ok {
			return errors.New("spell not found")
		}

		if spell.Level+1 > len(t.Levels) {
			return errors.New("wanted spell level doesn't exist")
		}

		if t.Levels[spell.Level].MinPlayerLevel > char.Level() {
			s.sendMessage(msgsvr.SpellsUpgradeSpellError{})
			return nil
		}
		level = char.Spells[i].Level + 1
		char.Spells[i].Level = level
		break
	}
	if !found {
		return errors.New("character doesn't know spell")
	}

	err = s.svr.retro.UpdateCharacter(ctx, char)
	if err != nil {
		return err
	}

	s.sendMessage(msgsvr.SpellsUpgradeSpellSuccess{
		Id:    m.Id,
		Level: level,
	})

	stats, err := s.protoStats(ctx)
	if err != nil {
		return err
	}
	s.sendMessage(stats)

	return nil
}

func (s *session) handleSpellsForget(ctx context.Context, m msgcli.SpellsForget) error {
	return s.forgetSpell(ctx, m.Id)
}

func (s *session) handleSpellsMoveToUsed(ctx context.Context, m msgcli.SpellsMoveToUsed) error {
	if m.Position < 0 || m.Position > 28 {
		return errors.New("invalid position")
	}

	char, err := s.svr.retro.Character(ctx, s.characterId)
	if err != nil {
		return err
	}

	for i, spell := range char.Spells {
		if spell.Position == m.Position {
			char.Spells[i].Position = 0
			break
		}
	}

	found := false
	for i, spell := range char.Spells {
		if spell.Id == m.Id {
			found = true
			char.Spells[i].Position = m.Position
			break
		}
	}
	if !found {
		return errors.New("character doesn't know spell")
	}

	err = s.svr.retro.UpdateCharacter(ctx, char)
	if err != nil {
		return err
	}

	s.sendMessage(msgsvr.BasicsNothing{})

	return nil
}

func (s *session) handleItemsUseNoConfirm(ctx context.Context, m msgcli.ItemsUseNoConfirm) error {
	if m.Id <= 0 {
		return errInvalidRequest
	}

	if m.SpriteId != 0 || m.Cell != 0 {
		return errNotImplemented
	}

	items, err := s.svr.retro.CharacterItemsByCharacterId(ctx, s.characterId)
	if err != nil {
		return err
	}

	item, ok := items[m.Id]
	if !ok {
		return errInvalidRequest
	}

	if item.Position != retrotyp.CharacterItemPositionInventory && (item.Position < 35 || item.Position > 62) {
		return errInvalidRequest
	}

	t, ok := s.svr.cache.static.items[item.TemplateId]
	if !ok {
		return errors.New("item template not found")
	}

	switch t.Type {
	case retrotyp.ItemTypeCandy:
		err := s.equip(ctx, item.Id, retrotyp.CharacterItemPositionBoostFood)
		if err != nil {
			return err
		}

		err = s.checkConditions(ctx)
		if err != nil {
			return err
		}

		err = s.sendWeight(ctx)
		if err != nil {
			return err
		}
	case retrotyp.ItemTypeUsableItem:
		switch t.Id {
		case 7651, 7799:
			char, err := s.svr.retro.Character(ctx, s.characterId)
			if err != nil {
				return err
			}

			err = s.mountOrDismount(ctx, !char.Mounting)
			if err != nil {
				return err
			}
		case 8626: // TODO: should change for custom item template
			char, err := s.svr.retro.Character(ctx, s.characterId)
			if err != nil {
				return err
			}

			mounts, err := s.svr.retro.MountsByCharacterId(ctx, s.characterId)
			if err != nil {
				return err
			}

			var shed []prototyp.CommonMountData
			for _, mount := range mounts {
				if mount.Id == char.MountId {
					continue
				}

				data, err := s.svr.commonMountData(mount)
				if err != nil {
					return err
				}
				shed = append(shed, data)
			}

			s.sendMessage(msgsvr.ExchangeCreateSuccess{
				Type: retrotyp.ExchangePaddock,
				Paddock: msgsvr.ExchangeCreateSuccessPaddock{
					Shed:    shed,
					Paddock: nil,
				},
			})
		default:
			return errNotImplemented
		}
	default:
		return errNotImplemented
	}

	return nil
}

func (s *session) handleEmotesSetDirection(ctx context.Context, m msgcli.EmotesSetDirection) error {
	if m.Dir < 0 || m.Dir > 7 {
		return errInvalidRequest
	}

	char, err := s.svr.retro.Character(ctx, s.characterId)
	if err != nil {
		return err
	}

	if m.Dir == char.Direction {
		s.sendMessage(msgsvr.BasicsNothing{})
		return nil
	}

	char.Direction = m.Dir

	err = s.svr.retro.UpdateCharacter(ctx, char)
	if err != nil {
		return err
	}

	err = s.svr.sendMsgToMap(ctx, char.GameMapId, msgsvr.EmotesDirection{
		Id:  char.Id,
		Dir: char.Direction,
	})
	if err != nil {
		return err
	}

	return nil
}

func (s *session) handleMountRequestData(ctx context.Context, m msgcli.MountRequestData) error {
	mount, err := s.svr.retro.Mount(ctx, m.Id)
	if err != nil {
		if errors.Is(err, retro.ErrNotFound) {
			s.sendMessage(msgsvr.InfosMessage{
				ChatId: protoenum.InfosMessageChatId.Error,
				Messages: []prototyp.InfosMessageMessage{
					{
						Id: 104,
					},
				},
			})
			return nil
		} else {
			return err
		}
	}

	if !m.Validity.Equal(mount.Validity) {
		return errInvalidRequest
	}

	mountData, err := s.svr.commonMountData(mount)
	if err != nil {
		return err
	}

	s.sendMessage(msgsvr.MountData{Data: mountData})

	return nil
}

func (s *session) handleMountRename(ctx context.Context, m msgcli.MountRename) error {
	err := validation.Validate(m.Name,
		validation.Required,
		validation.Length(1, 16),
		is.Alpha,
	)
	if err != nil {
		return err
	}

	char, err := s.svr.retro.Character(ctx, s.characterId)
	if err != nil {
		return err
	}

	if char.MountId == 0 {
		return errInvalidRequest
	}

	mount, err := s.svr.retro.Mount(ctx, char.MountId)
	if err != nil {
		return err
	}
	mount.Name = m.Name

	err = s.svr.retro.UpdateMount(ctx, mount)
	if err != nil {
		return err
	}

	s.sendMessage(msgsvr.MountName{Name: m.Name})

	return nil
}

func (s *session) handleMountFree(ctx context.Context) error {
	err := s.mountOrDismount(ctx, false)
	if err != nil {
		return err
	}

	char, err := s.svr.retro.Character(ctx, s.characterId)
	if err != nil {
		return err
	}

	originalMountId := char.MountId

	char.MountId = 0
	char.Kamas += 1
	err = s.svr.retro.UpdateCharacter(ctx, char)
	if err != nil {
		return err
	}

	err = s.svr.retro.DeleteMount(ctx, originalMountId)
	if err != nil {
		return err
	}

	s.sendMessage(msgsvr.MountUnequip{})

	stats, err := s.protoStats(ctx)
	if err != nil {
		return err
	}
	s.sendMessage(stats)

	return nil
}

func (s *session) handleMountRide(ctx context.Context) error {
	char, err := s.svr.retro.Character(ctx, s.characterId)
	if err != nil {
		return err
	}

	err = s.mountOrDismount(ctx, !char.Mounting)
	if err != nil {
		return err
	}

	return nil
}

func (s *session) handleGameActionsSendActions(ctx context.Context, m msgcli.GameActionsSendActions) error {
	switch m.ActionType {
	case protoenum.GameActionType.Movement:
		return s.actionMovement(ctx, m.ActionMovement)
	case protoenum.GameActionType.Challenge:
		return s.actionChallenge(ctx, m.ActionChallenge)
	case protoenum.GameActionType.ChallengeAccept:
		return s.actionChallengeAccept(ctx, m.ActionChallengeAccept)
	case protoenum.GameActionType.ChallengeRefuse:
		return s.actionChallengeRefuse(ctx, m.ActionChallengeRefuse)
	default:
		return errNotImplemented
	}
}

func (s *session) handleGameActionCancel(ctx context.Context, m msgcli.GameActionCancel) error {
	gameAction, ok := s.gameActions[m.Id]
	if !ok {
		return errInvalidRequest
	}

	if gameAction.ActionType != protoenum.GameActionType.Movement {
		return errNotImplemented
	}

	cell, err := strconv.Atoi(m.Params)
	if err != nil {
		return err
	}

	char, err := s.svr.retro.Character(ctx, s.characterId)
	if err != nil {
		return err
	}

	if char.Cell == cell {
		return errInvalidRequest
	}

	if len(gameAction.ActionMovement.DirAndCells) == 0 {
		return errors.New("empty directions and cells")
	}

	target := gameAction.ActionMovement.DirAndCells[len(gameAction.ActionMovement.DirAndCells)-1]
	if target.CellId == cell {
		return s.handleGameActionAck(ctx, msgcli.GameActionAck{Id: m.Id})
	}

	char.Cell = cell

	err = s.svr.retro.UpdateCharacter(ctx, char)
	if err != nil {
		return err
	}

	delete(s.gameActions, m.Id)
	s.busy.Dec()

	s.sendMessage(msgsvr.BasicsNothing{})

	return nil
}

func (s *session) handleGameActionAck(ctx context.Context, m msgcli.GameActionAck) error {
	gameAction, ok := s.gameActions[m.Id]
	if !ok {
		return errInvalidRequest
	}

	if gameAction.ActionType != protoenum.GameActionType.Movement {
		return errNotImplemented
	}

	char, err := s.svr.retro.Character(ctx, s.characterId)
	if err != nil {
		return err
	}

	final := gameAction.ActionMovement.DirAndCells[len(gameAction.ActionMovement.DirAndCells)-1]

	char.Cell = final.CellId
	char.Direction = final.DirId

	err = s.svr.retro.UpdateCharacter(ctx, char)
	if err != nil {
		return err
	}

	delete(s.gameActions, m.Id)
	s.busy.Dec()

	s.sendMessage(msgsvr.BasicsNothing{})

	trigger, err := s.svr.retro.TriggerByGameMapIdAndCellId(ctx, char.GameMapId, char.Cell)
	if err != nil {
		if !errors.Is(err, retro.ErrNotFound) {
			return err
		}
	} else {
		err = s.svr.sendMsgToMap(ctx, char.GameMapId, msgsvr.GameMovementRemove{Id: char.Id})
		if err != nil {
			return err
		}

		char.GameMapId = trigger.TargetGameMapId
		char.Cell = trigger.TargetCellId

		err = s.svr.retro.UpdateCharacter(ctx, char)
		if err != nil {
			return err
		}

		s.sendMessage(msgsvr.GameActions{
			ActionType: protoenum.GameActionType.LoadGameMap,
			ActionLoadGameMap: msgsvr.GameActionsActionLoadGameMap{
				SpriteId:  char.Id,
				Cinematic: 0,
			},
		})

		gameMap, ok := s.svr.cache.static.gameMaps[char.GameMapId]
		if !ok {
			return errors.New("invalid game map")
		}

		s.sendMessage(msgsvr.GameMapData{
			Id:   gameMap.Id,
			Name: gameMap.Name,
			Key:  gameMap.Key,
		})

		s.sendMessage(msgsvr.BasicsTime{Value: time.Now()})

		s.sendMessage(msgsvr.FightsCount{Value: 0}) // TODO

		sprite, err := s.svr.gameMovementSpriteCharacter(ctx, char, false)
		if err != nil {
			return err
		}

		err = s.svr.sendMsgToMap(ctx, char.GameMapId, msgsvr.GameMovement{Sprites: []msgsvr.GameMovementSprite{sprite}})
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *session) handleExchangePutInShedFromCertificate(ctx context.Context, m msgcli.ExchangePutInShedFromCertificate) error {
	charItem, err := s.svr.retro.CharacterItem(ctx, m.CertificateId)
	if err != nil {
		return err
	}

	if charItem.CharacterId != s.characterId {
		return errInvalidRequest
	}

	var mountId int
	var validity time.Time
	for _, effect := range charItem.Effects {
		if effect.Id == 995 {
			mountId = effect.DiceNum
			validity = time.Unix(int64(effect.DiceSide/1000), 0)
			break
		}
	}

	if validity.Before(time.Now()) {
		return errNotImplemented
	}

	err = s.removeItem(ctx, charItem.Id, 1)
	if err != nil {
		return err
	}

	mount, err := s.svr.retro.Mount(ctx, mountId)
	if err != nil {
		return err
	}
	mount.Validity = time.Time{}
	mount.CharacterId = s.characterId

	err = s.svr.retro.UpdateMount(ctx, mount)
	if err != nil {
		return err
	}

	data, err := s.svr.commonMountData(mount)
	if err != nil {
		return err
	}

	s.sendMessage(msgsvr.ExchangeMountStorageAdd{
		Data:    data,
		NewBorn: false,
	})

	return nil
}

func (s *session) handleExchangePutInShedFromInventory(ctx context.Context, m msgcli.ExchangePutInShedFromInventory) error {
	char, err := s.svr.retro.Character(ctx, s.characterId)
	if err != nil {
		return err
	}

	mount, err := s.svr.retro.Mount(ctx, m.MountId)
	if err != nil {
		return err
	}

	if mount.Id != char.MountId {
		return errInvalidRequest
	}

	if char.Mounting {
		err := s.mountOrDismount(ctx, false)
		if err != nil {
			return err
		}
	}

	s.sendMessage(msgsvr.MountUnequip{})

	char, err = s.svr.retro.Character(ctx, s.characterId)
	if err != nil {
		return err
	}
	char.MountId = 0

	err = s.svr.retro.UpdateCharacter(ctx, char)
	if err != nil {
		return err
	}

	data, err := s.svr.commonMountData(mount)
	if err != nil {
		return err
	}

	s.sendMessage(msgsvr.ExchangeMountStorageAdd{
		Data:    data,
		NewBorn: false,
	})

	return nil
}

func (s *session) handleExchangePutInCertificateFromShed(ctx context.Context, m msgcli.ExchangePutInCertificateFromShed) error {
	char, err := s.svr.retro.Character(ctx, s.characterId)
	if err != nil {
		return err
	}

	mount, err := s.svr.retro.Mount(ctx, m.MountId)
	if err != nil {
		return err
	}

	if mount.CharacterId != char.Id {
		return errInvalidRequest
	}

	if mount.Id == char.MountId {
		return errInvalidRequest
	}

	s.sendMessage(msgsvr.ExchangeMountStorageRemove{MountId: mount.Id})

	certificateId, ok := retro.MountCertificateIdByMountTemplateId[mount.TemplateId]
	if !ok {
		return errors.New("certificate id not found")
	}

	mount.CharacterId = 0
	mount.Validity = time.Now().Add(time.Hour * 24 * 20).Truncate(time.Minute)

	err = s.svr.retro.UpdateMount(ctx, mount)
	if err != nil {
		return err
	}

	effects := []retrotyp.Effect{
		{
			Id:       995,
			DiceNum:  mount.Id,
			DiceSide: int(mount.Validity.Unix()) * 1000,
		},
	}

	if mount.Name != "" {
		effects = append(effects, retrotyp.Effect{
			Id:    997,
			Param: mount.Name,
		})
	}

	item := retro.Item{
		TemplateId: certificateId,
		Quantity:   1,
		Effects:    effects,
	}

	_, err = s.addItemToInventory(ctx, item)
	if err != nil {
		return err
	}

	return nil
}

func (s *session) handleExchangePutInInventoryFromShed(ctx context.Context, m msgcli.ExchangePutInInventoryFromShed) error {
	char, err := s.svr.retro.Character(ctx, s.characterId)
	if err != nil {
		return err
	}

	if char.MountId != 0 {
		err = s.handleExchangePutInShedFromInventory(ctx, msgcli.ExchangePutInShedFromInventory{MountId: char.MountId})
		if err != nil {
			return err
		}
	}

	char, err = s.svr.retro.Character(ctx, s.characterId)
	if err != nil {
		return err
	}

	mount, err := s.svr.retro.Mount(ctx, m.MountId)
	if err != nil {
		return err
	}

	if mount.CharacterId != char.Id {
		return errInvalidRequest
	}

	if mount.Id == char.MountId {
		return errInvalidRequest
	}

	s.sendMessage(msgsvr.ExchangeMountStorageRemove{MountId: mount.Id})

	char.MountId = mount.Id
	err = s.svr.retro.UpdateCharacter(ctx, char)
	if err != nil {
		return err
	}

	data, err := s.svr.commonMountData(mount)
	if err != nil {
		return err
	}

	s.sendMessage(msgsvr.MountEquipSuccess{Data: data})

	if char.Level() >= 60 {
		err := s.mountOrDismount(ctx, true)
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *session) chatCommand(ctx context.Context, cmd string) error {
	if cmd == "" {
		return errors.New("command string is empty")
	}

	sli := strings.SplitN(cmd, " ", 2)

	cmd = strings.ToLower(sli[0])

	extra := ""
	if len(sli) >= 2 {
		extra = sli[1]
	}

	switch cmd {
	case "reset":
		err := s.resetCharacteristics(ctx)
		if err != nil {
			return err
		}

		s.sendMessage(msgsvr.ChatServerMessage{Message: "<b>Success</b>: Characteristics were reset."})
	case "level", "lvl":
		level, err := strconv.Atoi(extra)
		if err != nil {
			s.sendMessage(msgsvr.InfosMessage{
				ChatId: protoenum.InfosMessageChatId.Error,
				Messages: []prototyp.InfosMessageMessage{
					{
						Id:   16,
						Args: []string{"<b>Error</b>", err.Error()},
					},
				},
			})
			return nil
		}

		err = s.setLevel(ctx, level)
		if err != nil {
			return err
		}

		s.sendMessage(msgsvr.ChatServerMessage{Message: fmt.Sprintf("<b>Success</b>: Level set to %d.", level)})
	case "forget":
		s.sendMessage(msgsvr.SpellsSpellForgetShow{})
	default:
		s.sendMessage(msgsvr.InfosMessage{
			ChatId: protoenum.InfosMessageChatId.Error,
			Messages: []prototyp.InfosMessageMessage{
				{
					Id:   16,
					Args: []string{"<b>Error</b>", fmt.Sprintf("Command %q does not exist.", cmd)},
				},
			},
		})
	}

	return nil
}
