package retropvp

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
	"github.com/kralamoure/retro"
	"github.com/kralamoure/retro/retrotyp"
	"github.com/kralamoure/retroproto"
	protoenum "github.com/kralamoure/retroproto/enum"
	"github.com/kralamoure/retroproto/msgcli"
	"github.com/kralamoure/retroproto/msgsvr"
	prototyp "github.com/kralamoure/retroproto/typ"
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
	exchangeMarket *retro.Market
}

type msgOut interface {
	ProtocolId() (id retroproto.MsgSvrId)
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
				char, err := s.svr.retro.Character(ctx, s.characterId)
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

	id, ok := retroproto.MsgCliIdByPkt(pkt)
	name, _ := retroproto.MsgCliNameByID(id)
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
	case retroproto.AccountQueuePosition:
		err := s.handleAccountQueuePosition()
		if err != nil {
			return err
		}
	case retroproto.AksPing:
		err := s.handleAksPing()
		if err != nil {
			return err
		}
	case retroproto.AksQuickPing:
		err := s.handleAksQuickPing()
		if err != nil {
			return err
		}
	case retroproto.BasicsRequestAveragePing:
		err := s.handleBasicsRequestAveragePing()
		if err != nil {
			return err
		}
	case retroproto.BasicsGetDate:
		err := s.handleBasicsGetDate()
		if err != nil {
			return err
		}
	case retroproto.InfosSendScreenInfo:
		msg := msgcli.InfosSendScreenInfo{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleInfosSendScreenInfo(ctx, msg)
		if err != nil {
			return err
		}
	case retroproto.AccountSendTicket:
		msg := msgcli.AccountSendTicket{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleAccountSendTicket(ctx, msg)
		if err != nil {
			return err
		}
	case retroproto.AccountUseKey:
		msg := msgcli.AccountUseKey{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleAccountUseKey(msg)
		if err != nil {
			return err
		}
	case retroproto.AccountRequestRegionalVersion:
		err := s.handleAccountRequestRegionalVersion()
		if err != nil {
			return err
		}
	case retroproto.AccountGetGifts:
		err := s.handleAccountGetGifts()
		if err != nil {
			return err
		}
	case retroproto.AccountSendIdentity:
		msg := msgcli.AccountSendIdentity{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleAccountSendIdentity(ctx, msg)
		if err != nil {
			return err
		}
	case retroproto.AccountGetCharacters:
		err := s.handleAccountGetCharacters(ctx)
		if err != nil {
			return err
		}
	case retroproto.AccountGetCharactersForced:
		err := s.handleAccountGetCharactersForced(ctx)
		if err != nil {
			return err
		}
	case retroproto.AccountGetRandomCharacterName:
		err := s.handleAccountGetRandomCharacterName()
		if err != nil {
			return err
		}
	case retroproto.AccountSetCharacter:
		msg := msgcli.AccountSetCharacter{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleAccountSetCharacter(ctx, msg)
		if err != nil {
			return err
		}
	case retroproto.AccountAddCharacter:
		msg := msgcli.AccountAddCharacter{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleAccountAddCharacter(ctx, msg)
		if err != nil {
			return err
		}
	case retroproto.AccountDeleteCharacter:
		msg := msgcli.AccountDeleteCharacter{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleAccountDeleteCharacter(ctx, msg)
		if err != nil {
			return err
		}
	case retroproto.GameCreate:
		msg := msgcli.GameCreate{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleGameCreate(ctx, msg)
		if err != nil {
			return err
		}
	case retroproto.GameGetExtraInformations:
		err := s.handleGameGetExtraInformations(ctx)
		if err != nil {
			return err
		}
	case retroproto.ChatRequestSubscribeChannelAdd:
		msg := msgcli.ChatRequestSubscribeChannelAdd{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleChatRequestSubscribeChannelAdd(ctx, msg)
		if err != nil {
			return err
		}
	case retroproto.ChatRequestSubscribeChannelRemove:
		msg := msgcli.ChatRequestSubscribeChannelRemove{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleChatRequestSubscribeChannelRemove(ctx, msg)
		if err != nil {
			return err
		}
	case retroproto.ChatSend:
		msg := msgcli.ChatSend{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleChatSend(ctx, msg)
		if err != nil {
			return err
		}
	case retroproto.DialogCreate:
		msg := msgcli.DialogCreate{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleDialogCreate(ctx, msg)
		if err != nil {
			return err
		}
	case retroproto.DialogRequestLeave:
		err := s.handleDialogRequestLeave(ctx)
		if err != nil {
			return err
		}
	case retroproto.DialogResponse:
		msg := msgcli.DialogResponse{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleDialogResponse(ctx, msg)
		if err != nil {
			return err
		}
	case retroproto.ExchangeRequest:
		msg := msgcli.ExchangeRequest{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleExchangeRequest(ctx, msg)
		if err != nil {
			return err
		}
	case retroproto.ExchangeBigStoreType:
		msg := msgcli.ExchangeBigStoreType{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleExchangeBigStoreType(ctx, msg)
		if err != nil {
			return err
		}
	case retroproto.ExchangeBigStoreItemList:
		msg := msgcli.ExchangeBigStoreItemList{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleExchangeBigStoreItemList(ctx, msg)
		if err != nil {
			return err
		}
	case retroproto.ExchangeBigStoreSearch:
		msg := msgcli.ExchangeBigStoreSearch{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleExchangeBigStoreSearch(ctx, msg)
		if err != nil {
			return err
		}
	case retroproto.ExchangeGetItemMiddlePriceInBigStore:
		msg := msgcli.ExchangeGetItemMiddlePriceInBigStore{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleExchangeGetItemMiddlePriceInBigStore(ctx, msg)
		if err != nil {
			return err
		}
	case retroproto.ExchangeBigStoreBuy:
		msg := msgcli.ExchangeBigStoreBuy{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleExchangeBigStoreBuy(ctx, msg)
		if err != nil {
			return err
		}
	case retroproto.ExchangeLeave:
		err := s.handleExchangeLeave(ctx)
		if err != nil {
			return err
		}
	case retroproto.ItemsDestroy:
		msg := msgcli.ItemsDestroy{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleItemsDestroy(ctx, msg)
		if err != nil {
			return err
		}
	case retroproto.ItemsDrop:
		msg := msgcli.ItemsDrop{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleItemsDrop(ctx, msg)
		if err != nil {
			return err
		}
	case retroproto.ItemsRequestMovement:
		msg := msgcli.ItemsRequestMovement{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleItemsRequestMovement(ctx, msg)
		if err != nil {
			return err
		}
	case retroproto.AccountBoost:
		msg := msgcli.AccountBoost{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleAccountBoost(ctx, msg)
		if err != nil {
			return err
		}
	case retroproto.SpellsBoost:
		msg := msgcli.SpellsBoost{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleSpellsBoost(ctx, msg)
		if err != nil {
			return err
		}
	case retroproto.SpellsForget:
		msg := msgcli.SpellsForget{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleSpellsForget(ctx, msg)
		if err != nil {
			return err
		}
	case retroproto.SpellsMoveToUsed:
		msg := msgcli.SpellsMoveToUsed{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleSpellsMoveToUsed(ctx, msg)
		if err != nil {
			return err
		}
	case retroproto.ItemsUseNoConfirm:
		msg := msgcli.ItemsUseNoConfirm{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleItemsUseNoConfirm(ctx, msg)
		if err != nil {
			return err
		}
	case retroproto.EmotesSetDirection:
		msg := msgcli.EmotesSetDirection{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleEmotesSetDirection(ctx, msg)
		if err != nil {
			return err
		}
	case retroproto.MountRequestData:
		msg := msgcli.MountRequestData{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleMountRequestData(ctx, msg)
		if err != nil {
			return err
		}
	case retroproto.MountRename:
		msg := msgcli.MountRename{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleMountRename(ctx, msg)
		if err != nil {
			return err
		}
	case retroproto.MountFree:
		err := s.handleMountFree(ctx)
		if err != nil {
			return err
		}
	case retroproto.MountRide:
		err := s.handleMountRide(ctx)
		if err != nil {
			return err
		}
	case retroproto.GameActionsSendActions:
		msg := msgcli.GameActionsSendActions{}
		err := msg.Deserialize(extra)
		if errors.Is(err, retroproto.ErrNotImplemented) {
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
	case retroproto.GameActionCancel:
		msg := msgcli.GameActionCancel{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleGameActionCancel(ctx, msg)
		if err != nil {
			return err
		}
	case retroproto.GameActionAck:
		msg := msgcli.GameActionAck{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleGameActionAck(ctx, msg)
		if err != nil {
			return err
		}
	case retroproto.ExchangePutInShedFromCertificate:
		msg := msgcli.ExchangePutInShedFromCertificate{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleExchangePutInShedFromCertificate(ctx, msg)
		if err != nil {
			return err
		}
	case retroproto.ExchangePutInShedFromInventory:
		msg := msgcli.ExchangePutInShedFromInventory{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleExchangePutInShedFromInventory(ctx, msg)
		if err != nil {
			return err
		}
	case retroproto.ExchangePutInCertificateFromShed:
		msg := msgcli.ExchangePutInCertificateFromShed{}
		err := msg.Deserialize(extra)
		if err != nil {
			return err
		}
		err = s.handleExchangePutInCertificateFromShed(ctx, msg)
		if err != nil {
			return err
		}
	case retroproto.ExchangePutInInventoryFromShed:
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

func (s *session) frameMessage(id retroproto.MsgCliId) bool {
	switch id {
	case retroproto.AccountQueuePosition,
		retroproto.AksPing,
		retroproto.AksQuickPing,
		retroproto.BasicsRequestAveragePing,
		retroproto.BasicsGetDate,
		retroproto.InfosSendScreenInfo:
		return true
	}

	status := s.status.Load()
	switch status {
	case statusExpectingAccountSendTicket:
		if id != retroproto.AccountSendTicket {
			return false
		}
	case statusExpectingAccountUseKey:
		if id != retroproto.AccountUseKey {
			return false
		}
	case statusExpectingAccountRequestRegionalVersion:
		if id != retroproto.AccountRequestRegionalVersion {
			return false
		}
	case statusExpectingAccountGetGifts:
		if id != retroproto.AccountGetGifts {
			return false
		}
	case statusExpectingAccountSetCharacter:
		switch id {
		case retroproto.AccountSetCharacter,
			retroproto.AccountSendIdentity,
			retroproto.AccountGetCharacters,
			retroproto.AccountGetCharactersForced,
			retroproto.AccountAddCharacter,
			retroproto.AccountGetRandomCharacterName,
			retroproto.AccountDeleteCharacter:
		default:
			return false
		}
	case statusExpectingGameCreate:
		if id != retroproto.GameCreate {
			return false
		}
	}
	return true
}

func (s *session) sendMessage(msg msgOut) {
	pkt, err := msg.Serialized()
	if err != nil {
		name, _ := retroproto.MsgSvrNameByID(msg.ProtocolId())
		s.svr.logger.Errorw(fmt.Errorf("could not serialize message: %w", err).Error(),
			"name", name,
		)
		return
	}
	s.sendPacket(fmt.Sprint(msg.ProtocolId(), pkt))
}

func (s *session) sendPacket(pkt string) {
	id, _ := retroproto.MsgSvrIdByPkt(pkt)
	name, _ := retroproto.MsgSvrNameByID(id)
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

	char, err := s.svr.retro.Character(ctx, s.characterId)
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

	err = s.svr.retro.UpdateCharacter(ctx, char)
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
	if level < 1 || level > len(retro.CharacterXPFloors)+1 {
		return errors.New("invalid level")
	}

	char, err := s.svr.retro.Character(ctx, s.characterId)
	if err != nil {
		return err
	}

	originalLevel := char.Level()

	xp := 0
	if level >= 2 {
		xp = retro.CharacterXPFloors[level-2]
	}
	char.XP = xp

	err = s.svr.retro.UpdateCharacter(ctx, char)
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
						char.Spells = append(char.Spells, retro.CharacterSpell{
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

			err := s.svr.retro.UpdateCharacter(ctx, char)
			if err != nil {
				return err
			}
		}

		char, err = s.svr.retro.Character(ctx, s.characterId)
		if err != nil {
			return err
		}

		var spells []retro.CharacterSpell
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

	err = s.svr.retro.UpdateCharacter(ctx, char)
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
	char, err := s.svr.retro.Character(ctx, s.characterId)
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

	err = s.checkConditions(ctx)
	if err != nil {
		return err
	}

	return nil
}

func (s *session) mountOrDismount(ctx context.Context, mount bool) error {
	char, err := s.svr.retro.Character(ctx, s.characterId)
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
		charItems, err := s.svr.retro.CharacterItemsByCharacterId(ctx, s.characterId)
		if err != nil {
			return err
		}

		for _, v := range charItems {
			if v.Position == retrotyp.CharacterItemPositionPet {
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

	err = s.svr.retro.UpdateCharacter(ctx, char)
	if err != nil {
		return err
	}

	// Mount data is not changing (energy or tiredness, for example) in this type of server, so it's not necessary
	// to send MountEquipSuccess message.
	/*if mount {
		mount, err := s.svr.retro.Mount(ctx, char.MountId)
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

func (s *session) equip(ctx context.Context, id int, position retrotyp.CharacterItemPosition) error {
	if (position < retrotyp.CharacterItemPositionAmulet || position > retrotyp.CharacterItemPositionDragoturkey) &&
		(position < retrotyp.CharacterItemPositionMutationItem || position > retrotyp.CharacterItemPositionFollowingCharacter) {
		return errors.New("invalid desired position for item")
	}

	charItems, err := s.svr.retro.CharacterItemsByCharacterId(ctx, s.characterId)
	if err != nil {
		return err
	}

	for _, v := range charItems {
		if v.Position == position {
			err = s.unEquip(ctx, v.Id)
			break
		}
	}

	charItems, err = s.svr.retro.CharacterItemsByCharacterId(ctx, s.characterId)
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
	case retrotyp.ItemTypeAmulet:
		if position != retrotyp.CharacterItemPositionAmulet {
			wrongPosition = true
		}
	case retrotyp.ItemTypeRing:
		if position != retrotyp.CharacterItemPositionRingRight && position != retrotyp.CharacterItemPositionRingLeft {
			wrongPosition = true
		}
	case retrotyp.ItemTypeBelt:
		if position != retrotyp.CharacterItemPositionBelt {
			wrongPosition = true
		}
	case retrotyp.ItemTypeBoots:
		if position != retrotyp.CharacterItemPositionBoots {
			wrongPosition = true
		}
	case retrotyp.ItemTypeHat:
		if position != retrotyp.CharacterItemPositionHat {
			wrongPosition = true
		}
	case retrotyp.ItemTypeCloak:
		if position != retrotyp.CharacterItemPositionCloak {
			wrongPosition = true
		}
	case retrotyp.ItemTypePet:
		if position != retrotyp.CharacterItemPositionPet {
			wrongPosition = true
		}
	case retrotyp.ItemTypeDofus:
		if position < retrotyp.CharacterItemPositionDofus1 || position > retrotyp.CharacterItemPositionDofus6 {
			wrongPosition = true
		}
	case retrotyp.ItemTypeBackpack:
		if position != retrotyp.CharacterItemPositionCloak {
			wrongPosition = true
		}
	case retrotyp.ItemTypeShield:
		if position != retrotyp.CharacterItemPositionShield {
			wrongPosition = true
		}
	case retrotyp.ItemTypeBow,
		retrotyp.ItemTypeWand,
		retrotyp.ItemTypeStaff,
		retrotyp.ItemTypeDagger,
		retrotyp.ItemTypeSword,
		retrotyp.ItemTypeHammer,
		retrotyp.ItemTypeShovel,
		retrotyp.ItemTypeAxe,
		retrotyp.ItemTypeTool,
		retrotyp.ItemTypePickaxe,
		retrotyp.ItemTypeScythe,
		retrotyp.ItemTypeSoulStone,
		retrotyp.ItemTypeCrossbow,
		retrotyp.ItemTypeMagicWeapon:
		if position != retrotyp.CharacterItemPositionWeapon {
			wrongPosition = true
		}
	case retrotyp.ItemTypeCandy:
		if position != retrotyp.CharacterItemPositionBoostFood {
			wrongPosition = true
		}
	case retrotyp.ItemTypeMountCertificate:
		if position != retrotyp.CharacterItemPositionDragoturkey {
			wrongPosition = true
		}
	default:
		return errNotAllowed
	}
	if wrongPosition {
		return errNotAllowed
	}

	char, err := s.svr.retro.Character(ctx, s.characterId)
	if err != nil {
		return err
	}

	if template.Level > char.Level() {
		s.sendMessage(msgsvr.ItemsAddError{Reason: protoenum.ItemsAddErrorReason.TooLowLevelForItem})
		return nil
	}

	for _, v := range charItems {
		if v.Position < retrotyp.CharacterItemPositionAmulet || v.Position > retrotyp.CharacterItemPositionShield {
			continue
		}

		t, ok := s.svr.cache.static.items[v.TemplateId]
		if !ok {
			return errors.New("item template not found")
		}

		if t.Id == template.Id && (template.ItemSetId != 0 || template.Type == retrotyp.ItemTypeDofus) {
			s.sendMessage(msgsvr.ItemsAddError{Reason: protoenum.ItemsAddErrorReason.AlreadyEquipped})
			return nil
		}
	}

	switch position {
	case retrotyp.CharacterItemPositionWeapon:
		if template.TwoHands {
			for _, v := range charItems {
				if v.Position == retrotyp.CharacterItemPositionShield {
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
	case retrotyp.CharacterItemPositionShield:
		for _, v := range charItems {
			if v.Position == retrotyp.CharacterItemPositionWeapon {
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
	case retrotyp.CharacterItemPositionPet:
		if char.Mounting {
			err := s.mountOrDismount(ctx, false)
			if err != nil {
				return err
			}
		}
	}

	if position == retrotyp.CharacterItemPositionDragoturkey {
		if char.MountId != 0 {
			err := s.mountOrDismount(ctx, false)
			if err != nil {
				return err
			}

			char, err = s.svr.retro.Character(ctx, s.characterId)
			if err != nil {
				return err
			}

			mount, err := s.svr.retro.Mount(ctx, char.MountId)
			if err != nil {
				return err
			}

			char.MountId = 0
			err = s.svr.retro.UpdateCharacter(ctx, char)
			if err != nil {
				return err
			}

			s.sendMessage(msgsvr.MountUnequip{})

			mountCertificateId, ok := retro.MountCertificateIdByMountTemplateId[mount.TemplateId]
			if !ok {
				return errors.New("mount certificate id not found")
			}

			mountTemplate, ok := s.svr.cache.static.mounts[mount.TemplateId]
			if !ok {
				return errors.New("mount template not found")
			}

			mountCertificate := retro.CharacterItem{
				Item: retro.Item{
					TemplateId: mountCertificateId,
					Quantity:   1,
					Effects:    mountTemplate.Effects(mount.Level()),
				},
				Position:    retrotyp.CharacterItemPositionInventory,
				CharacterId: s.characterId,
			}

			mountCertificate.Effects = append(mountCertificate.Effects, retrotyp.Effect{
				Id:       995,
				DiceNum:  mount.Id,
				DiceSide: 0,
			})

			id, err := s.svr.retro.CreateCharacterItem(ctx, mountCertificate)
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

		err := s.svr.retro.DeleteCharacterItem(ctx, item.Id)
		if err != nil {
			return err
		}
		s.sendMessage(msgsvr.ItemsRemove{Id: item.Id})

		err = s.sendWeight(ctx)
		if err != nil {
			return err
		}

		char.MountId = mountId
		err = s.svr.retro.UpdateCharacter(ctx, char)
		if err != nil {
			return err
		}

		mount, err := s.svr.retro.Mount(ctx, char.MountId)
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

	stats, err := s.protoStats(ctx)
	if err != nil {
		return err
	}
	s.sendMessage(stats)

	return nil
}

func (s *session) unEquip(ctx context.Context, id int) error {
	item, err := s.svr.retro.CharacterItem(ctx, id)
	if err != nil {
		return err
	}

	if item.CharacterId != s.characterId {
		return errInvalidRequest
	}

	if (item.Position < retrotyp.CharacterItemPositionAmulet || item.Position > retrotyp.CharacterItemPositionDragoturkey) &&
		(item.Position != retrotyp.CharacterItemPositionBoostFood) {
		return errNotAllowed
	}

	err = s.moveItemToPosition(ctx, item.Id, item.Quantity, retrotyp.CharacterItemPositionInventory)
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

	stats, err := s.protoStats(ctx)
	if err != nil {
		return err
	}
	s.sendMessage(stats)

	return nil
}

func (s *session) moveItemToPosition(ctx context.Context, itemId, quantity int, position retrotyp.CharacterItemPosition) error {
	if (position < retrotyp.CharacterItemPositionInventory || position > retrotyp.CharacterItemPositionShield) &&
		(position < retrotyp.CharacterItemPositionMutationItem || position > retrotyp.CharacterItemPositionFollowingCharacter) &&
		(position < 35 || position > 62) {
		return errors.New("invalid position")
	}

	if quantity < 1 {
		return errors.New("invalid quantity")
	}

	charItems, err := s.svr.retro.CharacterItemsByCharacterId(ctx, s.characterId)
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

	otherItems := make(map[int]retro.Item)
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

		err = s.svr.retro.UpdateCharacterItem(ctx, charBatch)
		if err != nil {
			return err
		}

		s.sendMessage(msgsvr.ItemsQuantity{
			Id:       charBatch.Id,
			Quantity: charBatch.Quantity,
		})
	} else {
		if (position >= retrotyp.CharacterItemPositionAmulet && position <= retrotyp.CharacterItemPositionShield) ||
			(position >= retrotyp.CharacterItemPositionMutationItem && position <= retrotyp.CharacterItemPositionFollowingCharacter) {
			for _, v := range otherItems {
				err := s.unEquip(ctx, v.Id)
				if err != nil {
					return err
				}
			}
		} else if position != retrotyp.CharacterItemPositionInventory {
			for _, otherItem := range otherItems {
				err := s.moveItemToPosition(ctx, otherItem.Id, otherItem.Quantity, retrotyp.CharacterItemPositionInventory)
				if err != nil {
					return err
				}
			}
		}

		item, err = s.svr.retro.CharacterItem(ctx, itemId)
		if err != nil {
			return err
		}

		if quantity == item.Quantity {
			item.Position = position
			err := s.svr.retro.UpdateCharacterItem(ctx, item)
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

			newItem := retro.CharacterItem{
				Item: retro.Item{
					TemplateId: item.TemplateId,
					Quantity:   quantity,
					Effects:    item.Effects,
				},
				Position:    position,
				CharacterId: s.characterId,
			}

			id, err := s.svr.retro.CreateCharacterItem(ctx, newItem)
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

func (s *session) addItemToInventory(ctx context.Context, item retro.Item) (id int, err error) {
	var charItems map[int]retro.CharacterItem
	charItems, err = s.svr.retro.CharacterItemsByCharacterId(ctx, s.characterId)
	if err != nil {
		return
	}

	inventoryItems := make(map[int]retro.Item)
	for k, v := range charItems {
		if v.Position != retrotyp.CharacterItemPositionInventory {
			continue
		}
		inventoryItems[k] = v.Item
	}

	batch, ok := itemBatch(item, inventoryItems)
	if ok {
		charBatch := charItems[batch.Id]
		charBatch.Quantity += item.Quantity

		err = s.svr.retro.UpdateCharacterItem(ctx, charBatch)
		if err != nil {
			return
		}

		s.sendMessage(msgsvr.ItemsQuantity{
			Id:       charBatch.Id,
			Quantity: charBatch.Quantity,
		})
	} else {
		charItem := retro.CharacterItem{
			Item:        item,
			Position:    retrotyp.CharacterItemPositionInventory,
			CharacterId: s.characterId,
		}

		id, err = s.svr.retro.CreateCharacterItem(ctx, charItem)
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

	item, err := s.svr.retro.CharacterItem(ctx, id)
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
		err := s.svr.retro.DeleteCharacterItem(ctx, item.Id)
		if err != nil {
			return err
		}
		s.sendMessage(msgsvr.ItemsRemove{Id: item.Id})
	} else {
		item.Quantity -= quantity
		err := s.svr.retro.UpdateCharacterItem(ctx, item)
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

	positions := []retrotyp.CharacterItemPosition{
		retrotyp.CharacterItemPositionDragoturkey,

		retrotyp.CharacterItemPositionAmulet,
		retrotyp.CharacterItemPositionWeapon,
		retrotyp.CharacterItemPositionRingRight,
		retrotyp.CharacterItemPositionBelt,
		retrotyp.CharacterItemPositionRingLeft,
		retrotyp.CharacterItemPositionBoots,
		retrotyp.CharacterItemPositionHat,
		retrotyp.CharacterItemPositionCloak,
		retrotyp.CharacterItemPositionPet,
		retrotyp.CharacterItemPositionDofus1,
		retrotyp.CharacterItemPositionDofus2,
		retrotyp.CharacterItemPositionDofus3,
		retrotyp.CharacterItemPositionDofus4,
		retrotyp.CharacterItemPositionDofus5,
		retrotyp.CharacterItemPositionDofus6,
		retrotyp.CharacterItemPositionShield,

		retrotyp.CharacterItemPositionMutationItem,
		retrotyp.CharacterItemPositionBoostFood,
		retrotyp.CharacterItemPositionBlessing1,
		retrotyp.CharacterItemPositionBlessing2,
		retrotyp.CharacterItemPositionCurse1,
		retrotyp.CharacterItemPositionCurse2,
		retrotyp.CharacterItemPositionRoleplayBuff,
		retrotyp.CharacterItemPositionFollowingCharacter,
	}

	var changed bool

	for _, position := range positions {
		char, err := s.svr.retro.Character(ctx, s.characterId)
		if err != nil {
			return err
		}

		if position == retrotyp.CharacterItemPositionDragoturkey {
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

		var item retro.CharacterItem

		items, err := s.svr.retro.CharacterItemsByCharacterId(ctx, char.Id)
		if err != nil {
			return err
		}
		for k, v := range items {
			if v.Position == retrotyp.CharacterItemPositionInventory || v.Position >= 35 {
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
				string(retrotyp.ItemConditionMP): characteristics[retrotyp.CharacteristicIdMP].Total(),

				string(retrotyp.ItemConditionVitality):     characteristics[retrotyp.CharacteristicIdVitality].Total(),
				string(retrotyp.ItemConditionWisdom):       characteristics[retrotyp.CharacteristicIdWisdom].Total(),
				string(retrotyp.ItemConditionStrength):     characteristics[retrotyp.CharacteristicIdStrength].Total(),
				string(retrotyp.ItemConditionIntelligence): characteristics[retrotyp.CharacteristicIdIntelligence].Total(),
				string(retrotyp.ItemConditionChance):       characteristics[retrotyp.CharacteristicIdChance].Total(),
				string(retrotyp.ItemConditionAgility):      characteristics[retrotyp.CharacteristicIdAgility].Total(),

				string(retrotyp.ItemConditionVitalityBase):     characteristics[retrotyp.CharacteristicIdVitality].Base,
				string(retrotyp.ItemConditionWisdomBase):       characteristics[retrotyp.CharacteristicIdWisdom].Base,
				string(retrotyp.ItemConditionStrengthBase):     characteristics[retrotyp.CharacteristicIdStrength].Base,
				string(retrotyp.ItemConditionIntelligenceBase): characteristics[retrotyp.CharacteristicIdIntelligence].Base,
				string(retrotyp.ItemConditionChanceBase):       characteristics[retrotyp.CharacteristicIdChance].Base,
				string(retrotyp.ItemConditionAgilityBase):      characteristics[retrotyp.CharacteristicIdAgility].Base,

				string(retrotyp.ItemConditionName):       strings.ToLower(string(char.Name)), // It seems package gval v1.0.1 is bugged, so can't use InfixTextOperator
				string(retrotyp.ItemConditionSex):        int(char.Sex),
				string(retrotyp.ItemConditionClass):      int(char.ClassId),
				string(retrotyp.ItemConditionSubscriber): subscription,
				// string(retrotyp.ItemConditionKamas):      char.Kamas,
				string(retrotyp.ItemConditionKamas):   math.MaxInt32,
				string(retrotyp.ItemConditionItem):    itemTemplates,
				string(retrotyp.ItemConditionLevel):   char.Level(),
				string(retrotyp.ItemConditionWedding): 0,

				string(retrotyp.ItemConditionAlignment):               int(char.Alignment),
				string(retrotyp.ItemConditionAlignmentLevel):          100,
				string(retrotyp.ItemConditionAlignmentGift):           0,
				string(retrotyp.ItemConditionAlignmentSpecialization): 0,
				string(retrotyp.ItemConditionAlignmentGrade):          int(char.Grade()),

				string(retrotyp.ItemConditionUnusable): "",
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

	charItems, err := s.svr.retro.CharacterItemsByCharacterId(ctx, s.characterId)
	if err != nil {
		return err
	}

	var ids []int
	for _, v := range charItems {
		if v.Position == retrotyp.CharacterItemPositionInventory {
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
	char, err := s.svr.retro.Character(ctx, s.characterId)
	if err != nil {
		return err
	}

	items, err := s.svr.retro.CharacterItemsByCharacterId(ctx, s.characterId)
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
	var characteristics map[retrotyp.CharacteristicId]retrotyp.Characteristic
	characteristics, err = s.characteristics(ctx)
	if err != nil {
		return
	}

	maxWeight, ok := characteristics[retrotyp.CharacteristicIdMaxWeight]
	if !ok {
		err = errors.New("max weight characteristic not found")
		return
	}

	var items map[int]retro.CharacterItem
	items, err = s.svr.retro.CharacterItemsByCharacterId(ctx, s.characterId)
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
	char, err := s.svr.retro.Character(ctx, s.characterId)
	if err != nil {
		return
	}

	characteristics, err := s.characteristics(ctx)
	if err != nil {
		return
	}

	initiative := characteristics[retrotyp.CharacteristicIdInitiative]
	prospecting := characteristics[retrotyp.CharacteristicIdProspecting]

	lpMax := 50 + char.Level()*5 + characteristics[retrotyp.CharacteristicIdVitality].Total()

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

func (s *session) characteristics(ctx context.Context) (map[retrotyp.CharacteristicId]retrotyp.Characteristic, error) {
	char, err := s.svr.retro.Character(ctx, s.characterId)
	if err != nil {
		return nil, err
	}

	items, err := s.svr.retro.CharacterItemsByCharacterId(ctx, s.characterId)
	if err != nil {
		return nil, err
	}
	for k, v := range items {
		if v.Position < retrotyp.CharacterItemPositionAmulet || v.Position > retrotyp.CharacterItemPositionFollowingCharacter {
			delete(items, k)
		}
	}

	characteristics := make(map[retrotyp.CharacteristicId]retrotyp.Characteristic)

	for k := range retrotyp.CharacteristicIds {
		v := retrotyp.Characteristic{Id: k}
		switch k {
		case retrotyp.CharacteristicIdAP:
			base := 6
			if char.Level() >= 100 {
				base = 7
			}
			v.Base = base
		case retrotyp.CharacteristicIdMP:
			v.Base = 3
		case retrotyp.CharacteristicIdMaxSummonedCreaturesBoost:
			v.Base = 1
		case retrotyp.CharacteristicIdProspecting:
			base := 100
			if char.ClassId == retrotyp.ClassIdEnutrof {
				base += 20
			}
			v.Base = base
		case retrotyp.CharacteristicIdVitality:
			v.Base = char.Stats.Vitality
		case retrotyp.CharacteristicIdWisdom:
			v.Base = char.Stats.Wisdom
		case retrotyp.CharacteristicIdStrength:
			v.Base = char.Stats.Strength
		case retrotyp.CharacteristicIdIntelligence:
			v.Base = char.Stats.Intelligence
		case retrotyp.CharacteristicIdChance:
			v.Base = char.Stats.Chance
		case retrotyp.CharacteristicIdAgility:
			v.Base = char.Stats.Agility
		case retrotyp.CharacteristicIdMaxWeight:
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
			case retrotyp.EffectOperatorAdd:
				v.Equipment += effect.DiceNum
			case retrotyp.EffectOperatorSub:
				v.Equipment -= effect.DiceNum
			}

			characteristics[t.CharacteristicId] = v
		}
	}

	if char.Mounting {
		mount, err := s.svr.retro.Mount(ctx, char.MountId)
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
			case retrotyp.EffectOperatorAdd:
				v.Equipment += effect.DiceNum
			case retrotyp.EffectOperatorSub:
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
			case retrotyp.EffectOperatorAdd:
				v.Equipment += effect.DiceNum
			case retrotyp.EffectOperatorSub:
				v.Equipment -= effect.DiceNum
			}

			characteristics[t.CharacteristicId] = v
		}
	}

	initiative := characteristics[retrotyp.CharacteristicIdInitiative]
	initiative.Base += characteristics[retrotyp.CharacteristicIdStrength].Base
	initiative.Base += characteristics[retrotyp.CharacteristicIdIntelligence].Base
	initiative.Base += characteristics[retrotyp.CharacteristicIdChance].Base
	initiative.Base += characteristics[retrotyp.CharacteristicIdAgility].Base
	initiative.Equipment += characteristics[retrotyp.CharacteristicIdStrength].Equipment
	initiative.Equipment += characteristics[retrotyp.CharacteristicIdIntelligence].Equipment
	initiative.Equipment += characteristics[retrotyp.CharacteristicIdChance].Equipment
	initiative.Equipment += characteristics[retrotyp.CharacteristicIdAgility].Equipment
	initiative.Feat += characteristics[retrotyp.CharacteristicIdStrength].Feat
	initiative.Feat += characteristics[retrotyp.CharacteristicIdIntelligence].Feat
	initiative.Feat += characteristics[retrotyp.CharacteristicIdChance].Feat
	initiative.Feat += characteristics[retrotyp.CharacteristicIdAgility].Feat
	initiative.Boost += characteristics[retrotyp.CharacteristicIdStrength].Boost
	initiative.Boost += characteristics[retrotyp.CharacteristicIdIntelligence].Boost
	initiative.Boost += characteristics[retrotyp.CharacteristicIdChance].Boost
	initiative.Boost += characteristics[retrotyp.CharacteristicIdAgility].Boost
	characteristics[retrotyp.CharacteristicIdInitiative] = initiative

	prospecting := characteristics[retrotyp.CharacteristicIdProspecting]
	prospecting.Base += characteristics[retrotyp.CharacteristicIdChance].Base / 10
	prospecting.Equipment += characteristics[retrotyp.CharacteristicIdChance].Equipment / 10
	prospecting.Feat += characteristics[retrotyp.CharacteristicIdChance].Feat / 10
	prospecting.Boost += characteristics[retrotyp.CharacteristicIdChance].Boost / 10
	characteristics[retrotyp.CharacteristicIdProspecting] = prospecting

	dodgeAP := characteristics[retrotyp.CharacteristicIdDodgeAP]
	dodgeAP.Base += characteristics[retrotyp.CharacteristicIdWisdom].Base / 4
	dodgeAP.Equipment += characteristics[retrotyp.CharacteristicIdWisdom].Equipment / 4
	dodgeAP.Feat += characteristics[retrotyp.CharacteristicIdWisdom].Feat / 4
	dodgeAP.Boost += characteristics[retrotyp.CharacteristicIdWisdom].Boost / 4
	characteristics[retrotyp.CharacteristicIdDodgeAP] = dodgeAP

	dodgeMP := characteristics[retrotyp.CharacteristicIdDodgeMP]
	dodgeMP.Base += characteristics[retrotyp.CharacteristicIdWisdom].Base / 4
	dodgeMP.Equipment += characteristics[retrotyp.CharacteristicIdWisdom].Equipment / 4
	dodgeMP.Feat += characteristics[retrotyp.CharacteristicIdWisdom].Feat / 4
	dodgeMP.Boost += characteristics[retrotyp.CharacteristicIdWisdom].Boost / 4
	characteristics[retrotyp.CharacteristicIdDodgeMP] = dodgeMP

	maxWeight := characteristics[retrotyp.CharacteristicIdMaxWeight]
	maxWeight.Base += characteristics[retrotyp.CharacteristicIdStrength].Total() * 5
	characteristics[retrotyp.CharacteristicIdMaxWeight] = maxWeight

	return characteristics, nil
}
