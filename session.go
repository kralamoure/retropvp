package d1game

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/PaesslerAG/gval"
	"github.com/kralamoure/d1"
	"github.com/kralamoure/d1/d1typ"
	"github.com/kralamoure/d1proto"
	protoenum "github.com/kralamoure/d1proto/enum"
	"github.com/kralamoure/d1proto/msgcli"
	"github.com/kralamoure/d1proto/msgsvr"
	prototyp "github.com/kralamoure/d1proto/typ"
	"go.uber.org/atomic"
	"golang.org/x/time/rate"
)

const (
	statusExpectingAccountSendTicket uint32 = iota
	statusExpectingAccountUseKey
	statusExpectingAccountRequestRegionalVersion
	statusExpectingAccountGetGifts
	statusExpectingAccountSendIdentity
	statusExpectingAccountSetCharacter
	statusExpectingGameCreate
	statusExpectingGameGetExtraInformations

	statusIdle // TODO
)

var errNoop = errors.New("no-op")
var errInvalidRequest = errors.New("invalid request")
var errNotImplemented = errors.New("not implemented")
var errNotAllowed = errors.New("not allowed")

type session struct {
	svr         *Server
	conn        *net.TCPConn
	status      atomic.Uint32
	userId      string
	accountId   string
	characterId int

	cache sessionCache

	busy        atomic.Uint32
	gameActions map[int]msgsvr.GameActions
}

type sessionCache struct {
	exchangeMarket *d1.Market
}

type msgOut interface {
	ProtocolId() (id d1proto.MsgSvrId)
	Serialized() (extra string, err error)
}

func (s *session) receivePackets(ctx context.Context) error {
	lim := rate.NewLimiter(20, 1)

	rd := bufio.NewReaderSize(s.conn, 256)
	for {
		pkt, err := rd.ReadString('\x00')
		if err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				s.sendMessage(msgsvr.AksServerMessage{Value: "01"})
			}

			if s.characterId != 0 {
				char, err := s.svr.d1.Character(ctx, s.characterId)
				if err != nil {
					return err
				}

				err = s.svr.sendMsgToMap(ctx, char.GameMapId, msgsvr.GameMovementRemove{Id: char.Id})
				if err != nil {
					return err
				}
			}

			return err
		}
		err = lim.Wait(ctx)
		if err != nil {
			return err
		}
		pkt = strings.TrimSuffix(pkt, "\n\x00")
		if pkt == "" {
			continue
		}
		err = s.conn.SetReadDeadline(time.Now().UTC().Add(s.svr.connTimeout))
		if err != nil {
			return err
		}

		err = s.handlePacket(ctx, pkt)
		if err != nil {
			switch err {
			case errNoop:
				s.sendMessage(msgsvr.BasicsNothing{})
			case errNotImplemented:
				// TODO: hardcode in lang
				s.sendMessage(msgsvr.InfosMessage{
					ChatId: protoenum.InfosMessageChatId.Error,
					Messages: []prototyp.InfosMessageMessage{
						{
							Id:   16,
							Args: []string{"<b>Error</b>", "Not implemented."},
						},
					},
				})
			case errNotAllowed:
				// TODO: hardcode in lang
				s.sendMessage(msgsvr.InfosMessage{
					ChatId: protoenum.InfosMessageChatId.Error,
					Messages: []prototyp.InfosMessageMessage{
						{
							Id:   16,
							Args: []string{"<b>Error</b>", "Not allowed."},
						},
					},
				})
			default:
				return err
			}
		}
	}
}

func (s *session) handlePacket(ctx context.Context, pkt string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("recovered from panic: %v", r)
		}
	}()

	id, ok := d1proto.MsgCliIdByPkt(pkt)
	name, _ := d1proto.MsgCliNameByID(id)
	s.svr.logger.Infow("received packet from client",
		"message_name", name,
		"packet", pkt,
		"client_address", s.conn.RemoteAddr().String(),
	)
	if !ok {
		s.svr.logger.Debugw("unknown packet",
			"client_address", s.conn.RemoteAddr().String(),
		)
		return nil
	}
	extra := strings.TrimPrefix(pkt, string(id))

	if !s.frameMessage(id) {
		s.svr.logger.Debugw("invalid frame",
			"client_address", s.conn.RemoteAddr().String(),
		)
		return errInvalidRequest
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	switch id {
	case d1proto.AccountQueuePosition:
		err := s.handleAccountQueuePosition()
		if err != nil {
			return err
		}
	case d1proto.AksPing:
		err := s.handleAksPing()
		if err != nil {
			return err
		}
	case d1proto.AksQuickPing:
		err := s.handleAksQuickPing()
		if err != nil {
			return err
		}
	case d1proto.BasicsRequestAveragePing:
		err := s.handleBasicsRequestAveragePing()
		if err != nil {
			return err
		}
	case d1proto.BasicsGetDate:
		err := s.handleBasicsGetDate()
		if err != nil {
			return err
		}
	case d1proto.InfosSendScreenInfo:
		msg := msgcli.InfosSendScreenInfo{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleInfosSendScreenInfo(ctx, msg)
		if err != nil {
			return err
		}
	case d1proto.AccountSendTicket:
		msg := msgcli.AccountSendTicket{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleAccountSendTicket(ctx, msg)
		if err != nil {
			return err
		}
	case d1proto.AccountUseKey:
		msg := msgcli.AccountUseKey{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleAccountUseKey(msg)
		if err != nil {
			return err
		}
	case d1proto.AccountRequestRegionalVersion:
		err := s.handleAccountRequestRegionalVersion()
		if err != nil {
			return err
		}
	case d1proto.AccountGetGifts:
		err := s.handleAccountGetGifts()
		if err != nil {
			return err
		}
	case d1proto.AccountSendIdentity:
		msg := msgcli.AccountSendIdentity{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleAccountSendIdentity(ctx, msg)
		if err != nil {
			return err
		}
	case d1proto.AccountGetCharacters:
		err := s.handleAccountGetCharacters(ctx)
		if err != nil {
			return err
		}
	case d1proto.AccountGetCharactersForced:
		err := s.handleAccountGetCharactersForced(ctx)
		if err != nil {
			return err
		}
	case d1proto.AccountGetRandomCharacterName:
		err := s.handleAccountGetRandomCharacterName()
		if err != nil {
			return err
		}
	case d1proto.AccountSetCharacter:
		msg := msgcli.AccountSetCharacter{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleAccountSetCharacter(ctx, msg)
		if err != nil {
			return err
		}
	case d1proto.AccountAddCharacter:
		msg := msgcli.AccountAddCharacter{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleAccountAddCharacter(ctx, msg)
		if err != nil {
			return err
		}
	case d1proto.AccountDeleteCharacter:
		msg := msgcli.AccountDeleteCharacter{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleAccountDeleteCharacter(ctx, msg)
		if err != nil {
			return err
		}
	case d1proto.GameCreate:
		msg := msgcli.GameCreate{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleGameCreate(ctx, msg)
		if err != nil {
			return err
		}
	case d1proto.GameGetExtraInformations:
		err := s.handleGameGetExtraInformations(ctx)
		if err != nil {
			return err
		}
	case d1proto.ChatRequestSubscribeChannelAdd:
		msg := msgcli.ChatRequestSubscribeChannelAdd{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleChatRequestSubscribeChannelAdd(ctx, msg)
		if err != nil {
			return err
		}
	case d1proto.ChatRequestSubscribeChannelRemove:
		msg := msgcli.ChatRequestSubscribeChannelRemove{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleChatRequestSubscribeChannelRemove(ctx, msg)
		if err != nil {
			return err
		}
	case d1proto.ChatSend:
		msg := msgcli.ChatSend{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleChatSend(ctx, msg)
		if err != nil {
			return err
		}
	case d1proto.DialogCreate:
		msg := msgcli.DialogCreate{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleDialogCreate(ctx, msg)
		if err != nil {
			return err
		}
	case d1proto.DialogRequestLeave:
		err := s.handleDialogRequestLeave(ctx)
		if err != nil {
			return err
		}
	case d1proto.DialogResponse:
		msg := msgcli.DialogResponse{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleDialogResponse(ctx, msg)
		if err != nil {
			return err
		}
	case d1proto.ExchangeRequest:
		msg := msgcli.ExchangeRequest{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleExchangeRequest(ctx, msg)
		if err != nil {
			return err
		}
	case d1proto.ExchangeBigStoreType:
		msg := msgcli.ExchangeBigStoreType{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleExchangeBigStoreType(ctx, msg)
		if err != nil {
			return err
		}
	case d1proto.ExchangeBigStoreItemList:
		msg := msgcli.ExchangeBigStoreItemList{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleExchangeBigStoreItemList(ctx, msg)
		if err != nil {
			return err
		}
	case d1proto.ExchangeBigStoreSearch:
		msg := msgcli.ExchangeBigStoreSearch{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleExchangeBigStoreSearch(ctx, msg)
		if err != nil {
			return err
		}
	case d1proto.ExchangeGetItemMiddlePriceInBigStore:
		msg := msgcli.ExchangeGetItemMiddlePriceInBigStore{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleExchangeGetItemMiddlePriceInBigStore(ctx, msg)
		if err != nil {
			return err
		}
	case d1proto.ExchangeBigStoreBuy:
		msg := msgcli.ExchangeBigStoreBuy{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleExchangeBigStoreBuy(ctx, msg)
		if err != nil {
			return err
		}
	case d1proto.ExchangeLeave:
		err := s.handleExchangeLeave(ctx)
		if err != nil {
			return err
		}
	case d1proto.ItemsDestroy:
		msg := msgcli.ItemsDestroy{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleItemsDestroy(ctx, msg)
		if err != nil {
			return err
		}
	case d1proto.ItemsDrop:
		msg := msgcli.ItemsDrop{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleItemsDrop(ctx, msg)
		if err != nil {
			return err
		}
	case d1proto.ItemsRequestMovement:
		msg := msgcli.ItemsRequestMovement{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleItemsRequestMovement(ctx, msg)
		if err != nil {
			return err
		}
	case d1proto.AccountBoost:
		msg := msgcli.AccountBoost{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleAccountBoost(ctx, msg)
		if err != nil {
			return err
		}
	case d1proto.SpellsBoost:
		msg := msgcli.SpellsBoost{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleSpellsBoost(ctx, msg)
		if err != nil {
			return err
		}
	case d1proto.SpellsForget:
		msg := msgcli.SpellsForget{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleSpellsForget(ctx, msg)
		if err != nil {
			return err
		}
	case d1proto.SpellsMoveToUsed:
		msg := msgcli.SpellsMoveToUsed{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleSpellsMoveToUsed(ctx, msg)
		if err != nil {
			return err
		}
	case d1proto.ItemsUseNoConfirm:
		msg := msgcli.ItemsUseNoConfirm{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleItemsUseNoConfirm(ctx, msg)
		if err != nil {
			return err
		}
	case d1proto.EmotesSetDirection:
		msg := msgcli.EmotesSetDirection{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleEmotesSetDirection(ctx, msg)
		if err != nil {
			return err
		}
	case d1proto.MountRequestData:
		msg := msgcli.MountRequestData{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleMountRequestData(ctx, msg)
		if err != nil {
			return err
		}
	case d1proto.MountRename:
		msg := msgcli.MountRename{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleMountRename(ctx, msg)
		if err != nil {
			return err
		}
	case d1proto.MountFree:
		err := s.handleMountFree(ctx)
		if err != nil {
			return err
		}
	case d1proto.MountRide:
		err := s.handleMountRide(ctx)
		if err != nil {
			return err
		}
	case d1proto.GameActionsSendActions:
		msg := msgcli.GameActionsSendActions{}
		err := msg.Deserialize(extra)
		if errors.Is(err, d1proto.ErrNotImplemented) {
			s.sendMessage(msgsvr.InfosMessage{
				ChatId: protoenum.InfosMessageChatId.Error,
				Messages: []prototyp.InfosMessageMessage{
					{
						Id:   16,
						Args: []string{"<b>Error</b>", "Not implemented."},
					},
				},
			})
		} else if err != nil {
			return err
		} else {
			err = s.handleGameActionsSendActions(ctx, msg)
			if err != nil {
				return err
			}
		}
	case d1proto.GameActionCancel:
		msg := msgcli.GameActionCancel{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleGameActionCancel(ctx, msg)
		if err != nil {
			return err
		}
	case d1proto.GameActionAck:
		msg := msgcli.GameActionAck{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleGameActionAck(ctx, msg)
		if err != nil {
			return err
		}
	case d1proto.ExchangePutInShedFromCertificate:
		msg := msgcli.ExchangePutInShedFromCertificate{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleExchangePutInShedFromCertificate(ctx, msg)
		if err != nil {
			return err
		}
	case d1proto.ExchangePutInShedFromInventory:
		msg := msgcli.ExchangePutInShedFromInventory{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleExchangePutInShedFromInventory(ctx, msg)
		if err != nil {
			return err
		}
	case d1proto.ExchangePutInCertificateFromShed:
		msg := msgcli.ExchangePutInCertificateFromShed{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleExchangePutInCertificateFromShed(ctx, msg)
		if err != nil {
			return err
		}
	case d1proto.ExchangePutInInventoryFromShed:
		msg := msgcli.ExchangePutInInventoryFromShed{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleExchangePutInInventoryFromShed(ctx, msg)
		if err != nil {
			return err
		}
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
	}

	return nil
}

func (s *session) frameMessage(id d1proto.MsgCliId) bool {
	switch id {
	case d1proto.AccountQueuePosition,
		d1proto.AksPing,
		d1proto.AksQuickPing,
		d1proto.BasicsRequestAveragePing,
		d1proto.BasicsGetDate,
		d1proto.InfosSendScreenInfo:
		return true
	}

	status := s.status.Load()
	switch status {
	case statusExpectingAccountSendTicket:
		if id != d1proto.AccountSendTicket {
			return false
		}
	case statusExpectingAccountUseKey:
		if id != d1proto.AccountUseKey {
			return false
		}
	case statusExpectingAccountRequestRegionalVersion:
		if id != d1proto.AccountRequestRegionalVersion {
			return false
		}
	case statusExpectingAccountGetGifts:
		if id != d1proto.AccountGetGifts {
			return false
		}
	case statusExpectingAccountSetCharacter:
		switch id {
		case d1proto.AccountSetCharacter,
			d1proto.AccountSendIdentity,
			d1proto.AccountGetCharacters,
			d1proto.AccountGetCharactersForced,
			d1proto.AccountAddCharacter,
			d1proto.AccountGetRandomCharacterName,
			d1proto.AccountDeleteCharacter:
		default:
			return false
		}
	case statusExpectingGameCreate:
		if id != d1proto.GameCreate {
			return false
		}
	}
	return true
}

func (s *session) sendMessage(msg msgOut) {
	pkt, err := msg.Serialized()
	if err != nil {
		name, _ := d1proto.MsgSvrNameByID(msg.ProtocolId())
		s.svr.logger.Errorw(fmt.Errorf("could not serialize message: %w", err).Error(),
			"name", name,
		)
		return
	}
	s.sendPacket(fmt.Sprint(msg.ProtocolId(), pkt))
}

func (s *session) sendPacket(pkt string) {
	id, _ := d1proto.MsgSvrIdByPkt(pkt)
	name, _ := d1proto.MsgSvrNameByID(id)
	s.svr.logger.Infow("sent packet to client",
		"message_name", name,
		"packet", pkt,
		"client_address", s.conn.RemoteAddr().String(),
	)
	fmt.Fprint(s.conn, pkt+"\x00")
}

func (s *session) forgetSpell(ctx context.Context, id int) error {
	if id == -1 {
		s.sendMessage(msgsvr.SpellsSpellForgetClose{})
		return nil
	}

	char, err := s.svr.d1.Character(ctx, s.characterId)
	if err != nil {
		return err
	}

	var level int
	found := false
	for i, spell := range char.Spells {
		if spell.Id != id {
			continue
		}
		found = true
		level = spell.Level
		char.Spells[i].Level = 1
		break
	}
	if !found {
		return errors.New("character doesn't know spell")
	}

	err = s.svr.d1.UpdateCharacter(ctx, char)
	if err != nil {
		return err
	}

	s.sendMessage(msgsvr.SpellsUpgradeSpellSuccess{
		Id:    id,
		Level: 1,
	})

	stats, err := s.protoStats(ctx)
	if err != nil {
		return err
	}
	s.sendMessage(stats)

	s.sendMessage(msgsvr.InfosMessage{
		ChatId:   protoenum.InfosMessageChatId.Info,
		Messages: []prototyp.InfosMessageMessage{{Id: 154, Args: []string{fmt.Sprint(level), "0"}}},
	})

	return nil
}

func (s *session) setLevel(ctx context.Context, level int) error {
	if level < 1 || level > len(d1.CharacterXPFloors)+1 {
		return errors.New("invalid level")
	}

	char, err := s.svr.d1.Character(ctx, s.characterId)
	if err != nil {
		return err
	}

	originalLevel := char.Level()

	xp := 0
	if level >= 2 {
		xp = d1.CharacterXPFloors[level-2]
	}
	char.XP = xp

	err = s.svr.d1.UpdateCharacter(ctx, char)
	if err != nil {
		return err
	}

	if level >= originalLevel {
		char.BonusPoints += (level - originalLevel) * 5

		class, ok := s.svr.cache.static.classes[char.ClassId]
		if !ok {
			return errors.New("class not found")
		}

		for _, id := range class.Spells {
			t, ok := s.svr.cache.static.spells[id]
			if !ok {
				return errors.New("spell not found")
			}
			for _, spellLevel := range t.Levels {
				if spellLevel.MinPlayerLevel <= level {
					found := false
					for _, v := range char.Spells {
						if v.Id == id {
							found = true
							break
						}
					}
					if !found {
						char.Spells = append(char.Spells, d1.CharacterSpell{
							Id:       id,
							Level:    spellLevel.Grade,
							Position: 0,
						})
					}
				}
				break
			}
		}
	} else {
		maxBonusPoints := (level - 1) * 5
		usedBonusPoints := (originalLevel-1)*5 - char.BonusPoints

		if usedBonusPoints > maxBonusPoints {
			err := s.resetCharacteristics(ctx)
			if err != nil {
				return err
			}
		} else {
			char.BonusPoints = maxBonusPoints - usedBonusPoints

			err := s.svr.d1.UpdateCharacter(ctx, char)
			if err != nil {
				return err
			}
		}

		char, err = s.svr.d1.Character(ctx, s.characterId)
		if err != nil {
			return err
		}

		var spells []d1.CharacterSpell
		for _, spell := range char.Spells {
			t, ok := s.svr.cache.static.spells[spell.Id]
			if !ok {
				return errors.New("spell not found")
			}
			for _, v := range t.Levels {
				if v.MinPlayerLevel <= level {
					spells = append(spells, spell)
				}
				break
			}
		}
		char.Spells = spells
	}

	for i, spell := range char.Spells {
		t, ok := s.svr.cache.static.spells[spell.Id]
		if !ok {
			return errors.New("spell not found")
		}
		var maxSpellLevel int
		for i, spellLevel := range t.Levels {
			if spellLevel.MinPlayerLevel <= level {
				maxSpellLevel = i + 1
			} else {
				break
			}
		}
		spell.Level = maxSpellLevel
		char.Spells[i] = spell
	}

	err = s.svr.d1.UpdateCharacter(ctx, char)
	if err != nil {
		return err
	}

	s.sendMessage(msgsvr.AccountNewLevel{Level: level})

	s.sendMessage(msgsvr.SpellsList{Spells: char.Spells})

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

func (s *session) resetCharacteristics(ctx context.Context) error {
	char, err := s.svr.d1.Character(ctx, s.characterId)
	if err != nil {
		return err
	}

	char.Stats.Vitality = 101
	char.Stats.Wisdom = 101
	char.Stats.Strength = 101
	char.Stats.Intelligence = 101
	char.Stats.Chance = 101
	char.Stats.Agility = 101

	char.BonusPoints = (char.Level() - 1) * 5

	err = s.svr.d1.UpdateCharacter(ctx, char)
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

	err = s.checkConditions(ctx)
	if err != nil {
		return err
	}

	return nil
}

func (s *session) mountOrDismount(ctx context.Context, mount bool) error {
	char, err := s.svr.d1.Character(ctx, s.characterId)
	if err != nil {
		return err
	}

	if char.Mounting == mount {
		return nil
	}

	if mount && char.MountId == 0 {
		s.sendMessage(msgsvr.MountEquipError{Reason: protoenum.MountEquipErrorReason.Ride})
		return nil
	}

	if mount && char.Level() < 60 {
		s.sendMessage(msgsvr.MountEquipError{Reason: protoenum.MountEquipErrorReason.Ride})
		return nil
	}

	if mount {
		charItems, err := s.svr.d1.CharacterItemsByCharacterId(ctx, s.characterId)
		if err != nil {
			return err
		}

		for _, v := range charItems {
			if v.Position == d1typ.CharacterItemPositionPet {
				err = s.unEquip(ctx, v.Id)
				if err != nil {
					return err
				}

				s.sendMessage(msgsvr.InfosMessage{
					ChatId:   protoenum.InfosMessageChatId.Error,
					Messages: []prototyp.InfosMessageMessage{{Id: 88}},
				})
				break
			}
		}
	}

	char.Mounting = mount

	err = s.svr.d1.UpdateCharacter(ctx, char)
	if err != nil {
		return err
	}

	// Mount data is not changing (energy or tiredness, for example) in this type of server, so it's not necessary
	// to send MountEquipSuccess message.
	/*if mount {
		mount, err := s.svr.d1.Mount(ctx, char.MountId)
		if err != nil {
			return err
		}

		mountData, err := s.svr.commonMountData(mount)
		if err != nil {
			return err
		}

		s.sendMessage(msgsvr.MountEquipSuccess{Data: mountData})
	}*/

	s.sendMessage(msgsvr.MountRidingState{Riding: mount})

	err = s.sendWeight(ctx)
	if err != nil {
		return err
	}

	stats, err := s.protoStats(ctx)
	if err != nil {
		return err
	}
	s.sendMessage(stats)

	sprite, err := s.svr.gameMovementSpriteCharacter(ctx, char, true)
	if err != nil {
		return err
	}

	err = s.svr.sendMsgToMap(ctx, char.GameMapId, msgsvr.GameMovement{Sprites: []msgsvr.GameMovementSprite{sprite}})
	if err != nil {
		return err
	}

	err = s.checkConditions(ctx)
	if err != nil {
		return err
	}

	return nil
}

func (s *session) equip(ctx context.Context, id int, position d1typ.CharacterItemPosition) error {
	if (position < d1typ.CharacterItemPositionAmulet || position > d1typ.CharacterItemPositionDragoturkey) &&
		(position < d1typ.CharacterItemPositionMutationItem || position > d1typ.CharacterItemPositionFollowingCharacter) {
		return errors.New("invalid desired position for item")
	}

	charItems, err := s.svr.d1.CharacterItemsByCharacterId(ctx, s.characterId)
	if err != nil {
		return err
	}

	for _, v := range charItems {
		if v.Position == position {
			err = s.unEquip(ctx, v.Id)
			break
		}
	}

	charItems, err = s.svr.d1.CharacterItemsByCharacterId(ctx, s.characterId)
	if err != nil {
		return err
	}

	item, ok := charItems[id]
	if !ok {
		return errors.New("item not found")
	}

	template, ok := s.svr.cache.static.items[item.TemplateId]
	if !ok {
		return errors.New("item template not found")
	}

	wrongPosition := false
	switch template.Type {
	case d1typ.ItemTypeAmulet:
		if position != d1typ.CharacterItemPositionAmulet {
			wrongPosition = true
		}
	case d1typ.ItemTypeRing:
		if position != d1typ.CharacterItemPositionRingRight && position != d1typ.CharacterItemPositionRingLeft {
			wrongPosition = true
		}
	case d1typ.ItemTypeBelt:
		if position != d1typ.CharacterItemPositionBelt {
			wrongPosition = true
		}
	case d1typ.ItemTypeBoots:
		if position != d1typ.CharacterItemPositionBoots {
			wrongPosition = true
		}
	case d1typ.ItemTypeHat:
		if position != d1typ.CharacterItemPositionHat {
			wrongPosition = true
		}
	case d1typ.ItemTypeCloak:
		if position != d1typ.CharacterItemPositionCloak {
			wrongPosition = true
		}
	case d1typ.ItemTypePet:
		if position != d1typ.CharacterItemPositionPet {
			wrongPosition = true
		}
	case d1typ.ItemTypeDofus:
		if position < d1typ.CharacterItemPositionDofus1 || position > d1typ.CharacterItemPositionDofus6 {
			wrongPosition = true
		}
	case d1typ.ItemTypeBackpack:
		if position != d1typ.CharacterItemPositionCloak {
			wrongPosition = true
		}
	case d1typ.ItemTypeShield:
		if position != d1typ.CharacterItemPositionShield {
			wrongPosition = true
		}
	case d1typ.ItemTypeBow,
		d1typ.ItemTypeWand,
		d1typ.ItemTypeStaff,
		d1typ.ItemTypeDagger,
		d1typ.ItemTypeSword,
		d1typ.ItemTypeHammer,
		d1typ.ItemTypeShovel,
		d1typ.ItemTypeAxe,
		d1typ.ItemTypeTool,
		d1typ.ItemTypePickaxe,
		d1typ.ItemTypeScythe,
		d1typ.ItemTypeSoulStone,
		d1typ.ItemTypeCrossbow,
		d1typ.ItemTypeMagicWeapon:
		if position != d1typ.CharacterItemPositionWeapon {
			wrongPosition = true
		}
	case d1typ.ItemTypeCandy:
		if position != d1typ.CharacterItemPositionBoostFood {
			wrongPosition = true
		}
	case d1typ.ItemTypeMountCertificate:
		if position != d1typ.CharacterItemPositionDragoturkey {
			wrongPosition = true
		}
	default:
		return errNotAllowed
	}
	if wrongPosition {
		return errNotAllowed
	}

	char, err := s.svr.d1.Character(ctx, s.characterId)
	if err != nil {
		return err
	}

	if template.Level > char.Level() {
		s.sendMessage(msgsvr.ItemsAddError{Reason: protoenum.ItemsAddErrorReason.TooLowLevelForItem})
		return nil
	}

	for _, v := range charItems {
		if v.Position < d1typ.CharacterItemPositionAmulet || v.Position > d1typ.CharacterItemPositionShield {
			continue
		}

		t, ok := s.svr.cache.static.items[v.TemplateId]
		if !ok {
			return errors.New("item template not found")
		}

		if t.Id == template.Id && (template.ItemSetId != 0 || template.Type == d1typ.ItemTypeDofus) {
			s.sendMessage(msgsvr.ItemsAddError{Reason: protoenum.ItemsAddErrorReason.AlreadyEquipped})
			return nil
		}
	}

	switch position {
	case d1typ.CharacterItemPositionWeapon:
		if template.TwoHands {
			for _, v := range charItems {
				if v.Position == d1typ.CharacterItemPositionShield {
					err = s.unEquip(ctx, v.Id)
					if err != nil {
						return err
					}

					s.sendMessage(msgsvr.InfosMessage{
						ChatId:   protoenum.InfosMessageChatId.Info,
						Messages: []prototyp.InfosMessageMessage{{Id: 79}},
					})
					break
				}
			}
		}
	case d1typ.CharacterItemPositionShield:
		for _, v := range charItems {
			if v.Position == d1typ.CharacterItemPositionWeapon {
				t, ok := s.svr.cache.static.items[v.TemplateId]
				if !ok {
					return errors.New("item template not found")
				}
				if t.TwoHands {
					err = s.unEquip(ctx, v.Id)
					if err != nil {
						return err
					}

					s.sendMessage(msgsvr.InfosMessage{
						ChatId:   protoenum.InfosMessageChatId.Info,
						Messages: []prototyp.InfosMessageMessage{{Id: 78}},
					})
				}
				break
			}
		}
	case d1typ.CharacterItemPositionPet:
		if char.Mounting {
			err := s.mountOrDismount(ctx, false)
			if err != nil {
				return err
			}
		}
	}

	if position == d1typ.CharacterItemPositionDragoturkey {
		if char.MountId != 0 {
			err := s.mountOrDismount(ctx, false)
			if err != nil {
				return err
			}

			char, err = s.svr.d1.Character(ctx, s.characterId)
			if err != nil {
				return err
			}

			mount, err := s.svr.d1.Mount(ctx, char.MountId)
			if err != nil {
				return err
			}

			char.MountId = 0
			err = s.svr.d1.UpdateCharacter(ctx, char)
			if err != nil {
				return err
			}

			s.sendMessage(msgsvr.MountUnequip{})

			mountCertificateId, ok := d1.MountCertificateIdByMountTemplateId[mount.TemplateId]
			if !ok {
				return errors.New("mount certificate id not found")
			}

			mountTemplate, ok := s.svr.cache.static.mounts[mount.TemplateId]
			if !ok {
				return errors.New("mount template not found")
			}

			mountCertificate := d1.CharacterItem{
				Item: d1.Item{
					TemplateId: mountCertificateId,
					Quantity:   1,
					Effects:    mountTemplate.Effects(mount.Level()),
				},
				Position:    d1typ.CharacterItemPositionInventory,
				CharacterId: s.characterId,
			}

			mountCertificate.Effects = append(mountCertificate.Effects, d1typ.Effect{
				Id:       995,
				DiceNum:  mount.Id,
				DiceSide: 0,
			})

			id, err := s.svr.d1.CreateCharacterItem(ctx, mountCertificate)
			if err != nil {
				return err
			}
			mountCertificate.Id = id

			s.sendMessage(msgsvr.ItemsAddSuccess{Items: []msgsvr.ItemsAddSuccessItem{{
				ItemType: protoenum.ItemsAddSuccessItemType.Objects,
				Objects: []msgsvr.ItemsAddSuccessItemObject{
					{
						Id:         mountCertificate.Id,
						TemplateId: mountCertificate.TemplateId,
						Quantity:   mountCertificate.Quantity,
						Position:   mountCertificate.Position,
						Effects:    mountCertificate.DisplayEffects(),
					},
				},
			}}})
		}

		var mountId int
		for _, effect := range item.Effects {
			if effect.Id == 995 {
				mountId = effect.DiceNum
				break
			}
		}
		if mountId == 0 {
			return errors.New("mount id was not found in mount certificate")
		}

		err := s.svr.d1.DeleteCharacterItem(ctx, item.Id)
		if err != nil {
			return err
		}
		s.sendMessage(msgsvr.ItemsRemove{Id: item.Id})

		err = s.sendWeight(ctx)
		if err != nil {
			return err
		}

		char.MountId = mountId
		err = s.svr.d1.UpdateCharacter(ctx, char)
		if err != nil {
			return err
		}

		mount, err := s.svr.d1.Mount(ctx, char.MountId)
		if err != nil {
			return err
		}

		mountData, err := s.svr.commonMountData(mount)
		if err != nil {
			return err
		}

		s.sendMessage(msgsvr.MountEquipSuccess{Data: mountData})

		if char.Level() >= 60 {
			err = s.mountOrDismount(ctx, true)
			if err != nil {
				return err
			}
		}
	} else {
		err = s.moveItemToPosition(ctx, item.Id, 1, position)
		if err != nil {
			return err
		}
	}

	if template.ItemSetId != 0 {
		err := s.sendItemSetBonus(ctx, template.ItemSetId)
		if err != nil {
			return err
		}
	}

	switch position {
	case d1typ.CharacterItemPositionWeapon,
		d1typ.CharacterItemPositionHat,
		d1typ.CharacterItemPositionCloak,
		d1typ.CharacterItemPositionPet,
		d1typ.CharacterItemPositionShield:

		err := s.sendAccessories(ctx)
		if err != nil {
			return err
		}
	}

	stats, err := s.protoStats(ctx)
	if err != nil {
		return err
	}
	s.sendMessage(stats)

	return nil
}

func (s *session) unEquip(ctx context.Context, id int) error {
	item, err := s.svr.d1.CharacterItem(ctx, id)
	if err != nil {
		return err
	}

	if item.CharacterId != s.characterId {
		return errInvalidRequest
	}

	if (item.Position < d1typ.CharacterItemPositionAmulet || item.Position > d1typ.CharacterItemPositionDragoturkey) &&
		(item.Position != d1typ.CharacterItemPositionBoostFood) {
		return errNotAllowed
	}

	err = s.moveItemToPosition(ctx, item.Id, item.Quantity, d1typ.CharacterItemPositionInventory)
	if err != nil {
		return err
	}

	template, ok := s.svr.cache.static.items[item.TemplateId]
	if !ok {
		return errors.New("item template not found")
	}

	if template.ItemSetId != 0 {
		err := s.sendItemSetBonus(ctx, template.ItemSetId)
		if err != nil {
			return err
		}
	}

	switch item.Position {
	case d1typ.CharacterItemPositionWeapon,
		d1typ.CharacterItemPositionHat,
		d1typ.CharacterItemPositionCloak,
		d1typ.CharacterItemPositionPet,
		d1typ.CharacterItemPositionShield:

		err := s.sendAccessories(ctx)
		if err != nil {
			return err
		}
	}

	stats, err := s.protoStats(ctx)
	if err != nil {
		return err
	}
	s.sendMessage(stats)

	return nil
}

func (s *session) moveItemToPosition(ctx context.Context, itemId, quantity int, position d1typ.CharacterItemPosition) error {
	if (position < d1typ.CharacterItemPositionInventory || position > d1typ.CharacterItemPositionShield) &&
		(position < d1typ.CharacterItemPositionMutationItem || position > d1typ.CharacterItemPositionFollowingCharacter) &&
		(position < 35 || position > 62) {
		return errors.New("invalid position")
	}

	if quantity < 1 {
		return errors.New("invalid quantity")
	}

	charItems, err := s.svr.d1.CharacterItemsByCharacterId(ctx, s.characterId)
	if err != nil {
		return err
	}

	item, ok := charItems[itemId]
	if !ok {
		return errors.New("item not found")
	}

	if quantity > item.Quantity {
		return errors.New("invalid quantity")
	}

	otherItems := make(map[int]d1.Item)
	for _, v := range charItems {
		if v.Position != position {
			continue
		}

		if v.Id == item.Id {
			return errors.New("item is already in the position")
		}

		otherItems[v.Id] = v.Item
	}

	batch, ok := itemBatch(item.Item, otherItems)
	if ok {
		err := s.removeItem(ctx, item.Id, quantity)
		if err != nil {
			return err
		}

		charBatch := charItems[batch.Id]
		charBatch.Quantity += quantity

		err = s.svr.d1.UpdateCharacterItem(ctx, charBatch)
		if err != nil {
			return err
		}

		s.sendMessage(msgsvr.ItemsQuantity{
			Id:       charBatch.Id,
			Quantity: charBatch.Quantity,
		})
	} else {
		if (position >= d1typ.CharacterItemPositionAmulet && position <= d1typ.CharacterItemPositionShield) ||
			(position >= d1typ.CharacterItemPositionMutationItem && position <= d1typ.CharacterItemPositionFollowingCharacter) {
			for _, v := range otherItems {
				err := s.unEquip(ctx, v.Id)
				if err != nil {
					return err
				}
			}
		} else if position != d1typ.CharacterItemPositionInventory {
			for _, otherItem := range otherItems {
				err := s.moveItemToPosition(ctx, otherItem.Id, otherItem.Quantity, d1typ.CharacterItemPositionInventory)
				if err != nil {
					return err
				}
			}
		}

		item, err = s.svr.d1.CharacterItem(ctx, itemId)
		if err != nil {
			return err
		}

		if quantity == item.Quantity {
			item.Position = position
			err := s.svr.d1.UpdateCharacterItem(ctx, item)
			if err != nil {
				return err
			}

			s.sendMessage(msgsvr.ItemsMovement{
				Id:       item.Id,
				Position: item.Position,
			})
		} else {
			err := s.removeItem(ctx, item.Id, quantity)
			if err != nil {
				return err
			}

			newItem := d1.CharacterItem{
				Item: d1.Item{
					TemplateId: item.TemplateId,
					Quantity:   quantity,
					Effects:    item.Effects,
				},
				Position:    position,
				CharacterId: s.characterId,
			}

			id, err := s.svr.d1.CreateCharacterItem(ctx, newItem)
			if err != nil {
				return err
			}
			newItem.Id = id

			s.sendMessage(msgsvr.ItemsAddSuccess{Items: []msgsvr.ItemsAddSuccessItem{{
				ItemType: protoenum.ItemsAddSuccessItemType.Objects,
				Objects: []msgsvr.ItemsAddSuccessItemObject{
					{
						Id:         newItem.Id,
						TemplateId: newItem.TemplateId,
						Quantity:   newItem.Quantity,
						Position:   newItem.Position,
						Effects:    newItem.DisplayEffects(),
					},
				},
			}}})
		}
	}

	return nil
}

func (s *session) addItemToInventory(ctx context.Context, item d1.Item) (id int, err error) {
	var charItems map[int]d1.CharacterItem
	charItems, err = s.svr.d1.CharacterItemsByCharacterId(ctx, s.characterId)
	if err != nil {
		return
	}

	inventoryItems := make(map[int]d1.Item)
	for k, v := range charItems {
		if v.Position != d1typ.CharacterItemPositionInventory {
			continue
		}
		inventoryItems[k] = v.Item
	}

	batch, ok := itemBatch(item, inventoryItems)
	if ok {
		charBatch := charItems[batch.Id]
		charBatch.Quantity += item.Quantity

		err = s.svr.d1.UpdateCharacterItem(ctx, charBatch)
		if err != nil {
			return
		}

		s.sendMessage(msgsvr.ItemsQuantity{
			Id:       charBatch.Id,
			Quantity: charBatch.Quantity,
		})
	} else {
		charItem := d1.CharacterItem{
			Item:        item,
			Position:    d1typ.CharacterItemPositionInventory,
			CharacterId: s.characterId,
		}

		id, err = s.svr.d1.CreateCharacterItem(ctx, charItem)
		if err != nil {
			return
		}
		charItem.Id = id

		s.sendMessage(msgsvr.ItemsAddSuccess{Items: []msgsvr.ItemsAddSuccessItem{{
			ItemType: protoenum.ItemsAddSuccessItemType.Objects,
			Objects: []msgsvr.ItemsAddSuccessItemObject{
				{
					Id:         charItem.Id,
					TemplateId: charItem.TemplateId,
					Quantity:   charItem.Quantity,
					Position:   charItem.Position,
					Effects:    charItem.DisplayEffects(),
				},
			},
		}}})
	}

	return
}

func (s *session) removeItem(ctx context.Context, id, quantity int) error {
	if quantity < 1 {
		return errInvalidRequest
	}

	item, err := s.svr.d1.CharacterItem(ctx, id)
	if err != nil {
		return err
	}

	if item.CharacterId != s.characterId {
		return errors.New("character doesn't own item")
	}

	if quantity > item.Quantity {
		return errInvalidRequest
	}

	if quantity == item.Quantity {
		err := s.svr.d1.DeleteCharacterItem(ctx, item.Id)
		if err != nil {
			return err
		}
		s.sendMessage(msgsvr.ItemsRemove{Id: item.Id})
	} else {
		item.Quantity -= quantity
		err := s.svr.d1.UpdateCharacterItem(ctx, item)
		if err != nil {
			return err
		}
		s.sendMessage(msgsvr.ItemsQuantity{
			Id:       item.Id,
			Quantity: item.Quantity,
		})
	}

	return nil
}

func (s *session) checkConditions(ctx context.Context) error {
	account, err := s.svr.dofus.Account(ctx, s.accountId)
	if err != nil {
		return err
	}
	subscription := 0
	if !account.Subscription.Before(time.Now()) {
		subscription = 1
	}

	positions := []d1typ.CharacterItemPosition{
		d1typ.CharacterItemPositionDragoturkey,

		d1typ.CharacterItemPositionAmulet,
		d1typ.CharacterItemPositionWeapon,
		d1typ.CharacterItemPositionRingRight,
		d1typ.CharacterItemPositionBelt,
		d1typ.CharacterItemPositionRingLeft,
		d1typ.CharacterItemPositionBoots,
		d1typ.CharacterItemPositionHat,
		d1typ.CharacterItemPositionCloak,
		d1typ.CharacterItemPositionPet,
		d1typ.CharacterItemPositionDofus1,
		d1typ.CharacterItemPositionDofus2,
		d1typ.CharacterItemPositionDofus3,
		d1typ.CharacterItemPositionDofus4,
		d1typ.CharacterItemPositionDofus5,
		d1typ.CharacterItemPositionDofus6,
		d1typ.CharacterItemPositionShield,

		d1typ.CharacterItemPositionMutationItem,
		d1typ.CharacterItemPositionBoostFood,
		d1typ.CharacterItemPositionBlessing1,
		d1typ.CharacterItemPositionBlessing2,
		d1typ.CharacterItemPositionCurse1,
		d1typ.CharacterItemPositionCurse2,
		d1typ.CharacterItemPositionRoleplayBuff,
		d1typ.CharacterItemPositionFollowingCharacter,
	}

	var changed bool

	for _, position := range positions {
		char, err := s.svr.d1.Character(ctx, s.characterId)
		if err != nil {
			return err
		}

		if position == d1typ.CharacterItemPositionDragoturkey {
			if char.Level() < 60 && char.Mounting {
				s.sendMessage(msgsvr.MountEquipError{Reason: protoenum.MountEquipErrorReason.Ride})
				err = s.mountOrDismount(ctx, false)
				if err != nil {
					return err
				}
				changed = true
			}
			continue
		}

		var item d1.CharacterItem

		items, err := s.svr.d1.CharacterItemsByCharacterId(ctx, char.Id)
		if err != nil {
			return err
		}
		for k, v := range items {
			if v.Position == d1typ.CharacterItemPositionInventory || v.Position >= 35 {
				delete(items, k)
			} else if v.Position == position {
				item = v
			}
		}

		if item.Id == 0 {
			continue
		}

		itemTemplate, ok := s.svr.cache.static.items[item.TemplateId]
		if !ok {
			return errors.New("item template not found")
		}

		if itemTemplate.Level <= char.Level() {
			conditions := itemTemplate.Conditions

			if conditions == "" {
				continue
			}

			rx, err := regexp.Compile(`PJ[<>=]\d+(,\d+)?`)
			if err != nil {
				return err
			}
			conditions = rx.ReplaceAllString(conditions, "true")

			conditions = strings.ReplaceAll(conditions, "=", "==")
			conditions = strings.ReplaceAll(conditions, "~", "==")
			conditions = strings.ReplaceAll(conditions, "!", "!=")
			conditions = strings.ReplaceAll(conditions, "&", "&&")
			conditions = strings.ReplaceAll(conditions, "|", "||")

			conditions = strings.ReplaceAll(conditions, "PO==", "PO=*")
			conditions = strings.ReplaceAll(conditions, "PO!=", "PO!*")

			itemTemplates := make(map[int]struct{})
			for _, v := range items {
				itemTemplates[v.TemplateId] = struct{}{}
			}

			characteristics, err := s.characteristics(ctx)
			if err != nil {
				return err
			}

			params := map[string]interface{}{
				string(d1typ.ItemConditionMP): characteristics[d1typ.CharacteristicIdMP].Total(),

				string(d1typ.ItemConditionVitality):     characteristics[d1typ.CharacteristicIdVitality].Total(),
				string(d1typ.ItemConditionWisdom):       characteristics[d1typ.CharacteristicIdWisdom].Total(),
				string(d1typ.ItemConditionStrength):     characteristics[d1typ.CharacteristicIdStrength].Total(),
				string(d1typ.ItemConditionIntelligence): characteristics[d1typ.CharacteristicIdIntelligence].Total(),
				string(d1typ.ItemConditionChance):       characteristics[d1typ.CharacteristicIdChance].Total(),
				string(d1typ.ItemConditionAgility):      characteristics[d1typ.CharacteristicIdAgility].Total(),

				string(d1typ.ItemConditionVitalityBase):     characteristics[d1typ.CharacteristicIdVitality].Base,
				string(d1typ.ItemConditionWisdomBase):       characteristics[d1typ.CharacteristicIdWisdom].Base,
				string(d1typ.ItemConditionStrengthBase):     characteristics[d1typ.CharacteristicIdStrength].Base,
				string(d1typ.ItemConditionIntelligenceBase): characteristics[d1typ.CharacteristicIdIntelligence].Base,
				string(d1typ.ItemConditionChanceBase):       characteristics[d1typ.CharacteristicIdChance].Base,
				string(d1typ.ItemConditionAgilityBase):      characteristics[d1typ.CharacteristicIdAgility].Base,

				string(d1typ.ItemConditionName):       strings.ToLower(string(char.Name)), // It seems package gval v1.0.1 is bugged, so can't use InfixTextOperator
				string(d1typ.ItemConditionSex):        int(char.Sex),
				string(d1typ.ItemConditionClass):      int(char.ClassId),
				string(d1typ.ItemConditionSubscriber): subscription,
				// string(d1typ.ItemConditionKamas):      char.Kamas,
				string(d1typ.ItemConditionKamas):   math.MaxInt32,
				string(d1typ.ItemConditionItem):    itemTemplates,
				string(d1typ.ItemConditionLevel):   char.Level(),
				string(d1typ.ItemConditionWedding): 0,

				string(d1typ.ItemConditionAlignment):               int(char.Alignment),
				string(d1typ.ItemConditionAlignmentLevel):          100,
				string(d1typ.ItemConditionAlignmentGift):           0,
				string(d1typ.ItemConditionAlignmentSpecialization): 0,
				string(d1typ.ItemConditionAlignmentGrade):          int(char.Grade()),

				string(d1typ.ItemConditionUnusable): "",
			}

			l := gval.NewLanguage(gval.PropositionalLogic(), gval.Arithmetic(),
				gval.InfixOperator("=*", func(a, b interface{}) (interface{}, error) {
					_, ok := a.(map[int]struct{})[int(b.(float64))]
					return ok, nil
				}),
				gval.InfixOperator("!*", func(a, b interface{}) (interface{}, error) {
					_, ok := a.(map[int]struct{})[int(b.(float64))]
					return !ok, nil
				}),
			)

			eval, err := l.NewEvaluable(conditions)
			if err != nil {
				return err
			}

			pass, err := eval.EvalBool(ctx, params)
			if err != nil {
				return err
			}

			if pass {
				continue
			}
		}

		s.sendMessage(msgsvr.InfosMessage{
			ChatId:   protoenum.InfosMessageChatId.Error,
			Messages: []prototyp.InfosMessageMessage{{Id: 19}, {Id: 44}},
		})

		err = s.unEquip(ctx, item.Id)
		if err != nil {
			return err
		}

		changed = true
	}

	if changed {
		err := s.checkConditions(ctx)
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *session) sendItemSetBonus(ctx context.Context, id int) error {
	if id == 0 {
		return errors.New("invalid item set id")
	}

	charItems, err := s.svr.d1.CharacterItemsByCharacterId(ctx, s.characterId)
	if err != nil {
		return err
	}

	var ids []int
	for _, v := range charItems {
		if v.Position == d1typ.CharacterItemPositionInventory {
			continue
		}

		t, ok := s.svr.cache.static.items[v.TemplateId]
		if !ok {
			return errors.New("item template not found")
		}

		if t.ItemSetId == id {
			ids = append(ids, v.TemplateId)
		}
	}

	itemSet, ok := s.svr.cache.static.itemSets[id]
	if !ok {
		return errors.New("item set template not found")
	}

	if len(ids) == 0 {
		s.sendMessage(msgsvr.ItemsItemSetRemove{
			Id: id,
		})
	} else if len(itemSet.Bonus) < len(ids)-1 {
		return errors.New("invalid item set bonus index")
	} else {
		s.sendMessage(msgsvr.ItemsItemSetAdd{
			Id:                id,
			ItemsTemplatesIds: ids,
			Effects:           itemSet.Bonus[len(ids)-1],
		})
	}

	return nil
}

func (s *session) sendAccessories(ctx context.Context) error {
	char, err := s.svr.d1.Character(ctx, s.characterId)
	if err != nil {
		return err
	}

	items, err := s.svr.d1.CharacterItemsByCharacterId(ctx, s.characterId)
	if err != nil {
		return err
	}

	s.svr.sendMsgToMap(ctx, char.GameMapId, msgsvr.ItemsAccessories{
		Id:          char.Id,
		Accessories: protoAccessories(items),
	})

	return nil
}

func (s *session) sendWeight(ctx context.Context) error {
	weight, err := s.protoWeight(ctx)
	if err != nil {
		return err
	}

	s.sendMessage(weight)

	return nil
}

func (s *session) protoWeight(ctx context.Context) (weight msgsvr.ItemsWeight, err error) {
	var characteristics map[d1typ.CharacteristicId]d1typ.Characteristic
	characteristics, err = s.characteristics(ctx)
	if err != nil {
		return
	}

	maxWeight, ok := characteristics[d1typ.CharacteristicIdMaxWeight]
	if !ok {
		err = errors.New("max weight characteristic not found")
		return
	}

	var items map[int]d1.CharacterItem
	items, err = s.svr.d1.CharacterItemsByCharacterId(ctx, s.characterId)
	if err != nil {
		return
	}

	var current int
	for _, item := range items {
		t, ok := s.svr.cache.static.items[item.TemplateId]
		if !ok {
			err = errors.New("item template not found")
			return
		}

		current += t.Weight * item.Quantity
	}

	max := maxWeight.Total()
	if max < 0 {
		max = 0
	}

	weight = msgsvr.ItemsWeight{
		Current: current,
		Max:     max,
	}

	return
}

func (s *session) protoStats(ctx context.Context) (stats msgsvr.AccountStats, err error) {
	char, err := s.svr.d1.Character(ctx, s.characterId)
	if err != nil {
		return
	}

	characteristics, err := s.characteristics(ctx)
	if err != nil {
		return
	}

	initiative := characteristics[d1typ.CharacteristicIdInitiative]
	prospecting := characteristics[d1typ.CharacteristicIdProspecting]

	lpMax := 50 + char.Level()*5 + characteristics[d1typ.CharacteristicIdVitality].Total()

	stats = msgsvr.AccountStats{
		XP:               char.XP,
		XPLow:            char.XPLow(),
		XPHigh:           char.XPHigh(),
		Kama:             char.Kamas,
		BonusPoints:      char.BonusPoints,
		BonusPointsSpell: char.BonusPointsSpell,
		Alignment:        int(char.Alignment),
		FakeAlignment:    int(char.Alignment),
		AlignmentLevel:   0,
		Grade:            int(char.Grade()),
		Honour:           char.Honor,
		Disgrace:         char.Disgrace,
		AlignmentEnabled: char.AlignmentEnabled,
		LP:               lpMax,
		LPMax:            lpMax,
		Energy:           10000,
		EnergyMax:        10000,
		Initiative:       initiative.Total(),
		Discernment:      prospecting.Total(),
		Characteristics:  characteristics,
	}
	return
}

func (s *session) characteristics(ctx context.Context) (map[d1typ.CharacteristicId]d1typ.Characteristic, error) {
	char, err := s.svr.d1.Character(ctx, s.characterId)
	if err != nil {
		return nil, err
	}

	items, err := s.svr.d1.CharacterItemsByCharacterId(ctx, s.characterId)
	if err != nil {
		return nil, err
	}
	for k, v := range items {
		if v.Position < d1typ.CharacterItemPositionAmulet || v.Position > d1typ.CharacterItemPositionFollowingCharacter {
			delete(items, k)
		}
	}

	characteristics := make(map[d1typ.CharacteristicId]d1typ.Characteristic)

	for k := range d1typ.CharacteristicIds {
		v := d1typ.Characteristic{Id: k}
		switch k {
		case d1typ.CharacteristicIdAP:
			base := 6
			if char.Level() >= 100 {
				base = 7
			}
			v.Base = base
		case d1typ.CharacteristicIdMP:
			v.Base = 3
		case d1typ.CharacteristicIdMaxSummonedCreaturesBoost:
			v.Base = 1
		case d1typ.CharacteristicIdProspecting:
			base := 100
			if char.ClassId == d1typ.ClassIdEnutrof {
				base += 20
			}
			v.Base = base
		case d1typ.CharacteristicIdVitality:
			v.Base = char.Stats.Vitality
		case d1typ.CharacteristicIdWisdom:
			v.Base = char.Stats.Wisdom
		case d1typ.CharacteristicIdStrength:
			v.Base = char.Stats.Strength
		case d1typ.CharacteristicIdIntelligence:
			v.Base = char.Stats.Intelligence
		case d1typ.CharacteristicIdChance:
			v.Base = char.Stats.Chance
		case d1typ.CharacteristicIdAgility:
			v.Base = char.Stats.Agility
		case d1typ.CharacteristicIdMaxWeight:
			v.Base = 1000 + char.Level()*5
		}

		characteristics[k] = v
	}

	for _, item := range items {
		for _, effect := range item.Effects {
			t, ok := s.svr.cache.static.effects[effect.Id]
			if !ok {
				return nil, errors.New("effect template not found")
			}

			if t.CharacteristicId <= 0 {
				continue
			}

			v, ok := characteristics[t.CharacteristicId]
			if !ok {
				continue
			}

			switch t.Operator {
			case d1typ.EffectOperatorAdd:
				v.Equipment += effect.DiceNum
			case d1typ.EffectOperatorSub:
				v.Equipment -= effect.DiceNum
			}

			characteristics[t.CharacteristicId] = v
		}
	}

	if char.Mounting {
		mount, err := s.svr.d1.Mount(ctx, char.MountId)
		if err != nil {
			return nil, err
		}

		mountTemplate, ok := s.svr.cache.static.mounts[mount.TemplateId]
		if !ok {
			return nil, errors.New("mount template not found")
		}

		for _, effect := range mountTemplate.Effects(mount.Level()) {
			t, ok := s.svr.cache.static.effects[effect.Id]
			if !ok {
				return nil, errors.New("effect template not found")
			}

			if t.CharacteristicId <= 0 {
				continue
			}

			v, ok := characteristics[t.CharacteristicId]
			if !ok {
				continue
			}

			switch t.Operator {
			case d1typ.EffectOperatorAdd:
				v.Equipment += effect.DiceNum
			case d1typ.EffectOperatorSub:
				v.Equipment -= effect.DiceNum
			}

			characteristics[t.CharacteristicId] = v
		}
	}

	itemSetIds := make(map[int]int)
	for _, item := range items {
		t, ok := s.svr.cache.static.items[item.TemplateId]
		if !ok {
			return nil, errors.New("item template not found")
		}
		if t.ItemSetId == 0 {
			continue
		}
		itemSetIds[t.ItemSetId]++
	}

	for id, quantity := range itemSetIds {
		t, ok := s.svr.cache.static.itemSets[id]
		if !ok {
			return nil, errors.New("item set template not found")
		}
		ix := quantity - 1
		if ix >= len(t.Bonus) {
			return nil, errors.New("invalid item set bonus index")
		}
		effects := t.Bonus[ix]

		for _, effect := range effects {
			t, ok := s.svr.cache.static.effects[effect.Id]
			if !ok {
				return nil, errors.New("effect template not found")
			}

			if t.CharacteristicId <= 0 {
				continue
			}

			v, ok := characteristics[t.CharacteristicId]
			if !ok {
				continue
			}

			switch t.Operator {
			case d1typ.EffectOperatorAdd:
				v.Equipment += effect.DiceNum
			case d1typ.EffectOperatorSub:
				v.Equipment -= effect.DiceNum
			}

			characteristics[t.CharacteristicId] = v
		}
	}

	initiative := characteristics[d1typ.CharacteristicIdInitiative]
	initiative.Base += characteristics[d1typ.CharacteristicIdStrength].Base
	initiative.Base += characteristics[d1typ.CharacteristicIdIntelligence].Base
	initiative.Base += characteristics[d1typ.CharacteristicIdChance].Base
	initiative.Base += characteristics[d1typ.CharacteristicIdAgility].Base
	initiative.Equipment += characteristics[d1typ.CharacteristicIdStrength].Equipment
	initiative.Equipment += characteristics[d1typ.CharacteristicIdIntelligence].Equipment
	initiative.Equipment += characteristics[d1typ.CharacteristicIdChance].Equipment
	initiative.Equipment += characteristics[d1typ.CharacteristicIdAgility].Equipment
	initiative.Feat += characteristics[d1typ.CharacteristicIdStrength].Feat
	initiative.Feat += characteristics[d1typ.CharacteristicIdIntelligence].Feat
	initiative.Feat += characteristics[d1typ.CharacteristicIdChance].Feat
	initiative.Feat += characteristics[d1typ.CharacteristicIdAgility].Feat
	initiative.Boost += characteristics[d1typ.CharacteristicIdStrength].Boost
	initiative.Boost += characteristics[d1typ.CharacteristicIdIntelligence].Boost
	initiative.Boost += characteristics[d1typ.CharacteristicIdChance].Boost
	initiative.Boost += characteristics[d1typ.CharacteristicIdAgility].Boost
	characteristics[d1typ.CharacteristicIdInitiative] = initiative

	prospecting := characteristics[d1typ.CharacteristicIdProspecting]
	prospecting.Base += characteristics[d1typ.CharacteristicIdChance].Base / 10
	prospecting.Equipment += characteristics[d1typ.CharacteristicIdChance].Equipment / 10
	prospecting.Feat += characteristics[d1typ.CharacteristicIdChance].Feat / 10
	prospecting.Boost += characteristics[d1typ.CharacteristicIdChance].Boost / 10
	characteristics[d1typ.CharacteristicIdProspecting] = prospecting

	dodgeAP := characteristics[d1typ.CharacteristicIdDodgeAP]
	dodgeAP.Base += characteristics[d1typ.CharacteristicIdWisdom].Base / 4
	dodgeAP.Equipment += characteristics[d1typ.CharacteristicIdWisdom].Equipment / 4
	dodgeAP.Feat += characteristics[d1typ.CharacteristicIdWisdom].Feat / 4
	dodgeAP.Boost += characteristics[d1typ.CharacteristicIdWisdom].Boost / 4
	characteristics[d1typ.CharacteristicIdDodgeAP] = dodgeAP

	dodgeMP := characteristics[d1typ.CharacteristicIdDodgeMP]
	dodgeMP.Base += characteristics[d1typ.CharacteristicIdWisdom].Base / 4
	dodgeMP.Equipment += characteristics[d1typ.CharacteristicIdWisdom].Equipment / 4
	dodgeMP.Feat += characteristics[d1typ.CharacteristicIdWisdom].Feat / 4
	dodgeMP.Boost += characteristics[d1typ.CharacteristicIdWisdom].Boost / 4
	characteristics[d1typ.CharacteristicIdDodgeMP] = dodgeMP

	maxWeight := characteristics[d1typ.CharacteristicIdMaxWeight]
	maxWeight.Base += characteristics[d1typ.CharacteristicIdStrength].Total() * 5
	characteristics[d1typ.CharacteristicIdMaxWeight] = maxWeight

	return characteristics, nil
}
