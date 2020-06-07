package handle

import (
	"fmt"
	"net"
	"time"

	"github.com/kralamoure/d1/filter"
	"github.com/kralamoure/d1proto/enum"
	"github.com/kralamoure/d1proto/msgcli"
	"github.com/kralamoure/d1proto/msgsvr"
	"github.com/kralamoure/d1proto/typ"

	"github.com/kralamoure/d1game"
)

func GameCreate(svr *d1game.Server, sess *d1game.Session, msg msgcli.GameCreate) error {
	if msg.Type != enum.GameCreateType.Solo {
		return fmt.Errorf("wrong game create type: %d", msg.Type)
	}

	account, err := svr.Login.Account(filter.AccountIdEQ(sess.AccountId))
	if err != nil {
		return err
	}

	user, err := svr.Login.User(filter.UserIdEQ(account.UserId))
	if err != nil {
		return err
	}

	svr.SendPacketMsg(sess.Conn, &msgsvr.SpecializationSet{Value: 0})

	chatChannels := make([]rune, len(user.ChatChannels))
	for i := range user.ChatChannels {
		chatChannels[i] = rune(user.ChatChannels[i])
	}
	svr.SendPacketMsg(sess.Conn, &msgsvr.ChatSubscribeChannelAdd{Channels: chatChannels})

	svr.SendPacketMsg(sess.Conn, &msgsvr.SpellsChangeOption{CanUseSeeAllSpell: true})

	svr.SendPacketMsg(sess.Conn, &msgsvr.AccountRestrictions{
		Restrictions: typ.CommonRestrictions{
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
	})

	svr.SendPacketMsg(sess.Conn, &msgsvr.ItemsWeight{Current: 0, Max: 1000})
	svr.SendPacketMsg(sess.Conn, &msgsvr.FriendsNotifyChange{Notify: true})

	svr.SendPacketMsg(sess.Conn, &msgsvr.InfosMessage{
		ChatId: enum.InfosMessageChatId.Error,
		Messages: []typ.InfosMessageMessage{
			{
				Id:   89,
				Args: nil,
			},
		},
	})

	if !sess.LastAccess.IsZero() && sess.LastIP != "" {
		svr.SendPacketMsg(sess.Conn, &msgsvr.InfosMessage{
			ChatId: enum.InfosMessageChatId.Info,
			Messages: []typ.InfosMessageMessage{
				{
					Id: 152,
					Args: []string{
						fmt.Sprintf("%d", sess.LastAccess.Year()),
						fmt.Sprintf("%d", sess.LastAccess.Month()),
						fmt.Sprintf("%d", sess.LastAccess.Day()),
						fmt.Sprintf("%d", sess.LastAccess.Hour()),
						fmt.Sprintf("%02d", sess.LastAccess.Minute()),
						sess.LastIP,
					},
				},
			},
		})
	} else {
		svr.SendPacketMsg(sess.Conn, &msgsvr.InfosMessage{
			ChatId: enum.InfosMessageChatId.Info,
			Messages: []typ.InfosMessageMessage{
				{
					Id: 152,
					Args: []string{
						fmt.Sprintf("%d", account.LastAccess.Year()),
						fmt.Sprintf("%d", account.LastAccess.Month()),
						fmt.Sprintf("%d", account.LastAccess.Day()),
						fmt.Sprintf("%d", account.LastAccess.Hour()),
						fmt.Sprintf("%02d", account.LastAccess.Minute()),
						account.LastIP,
					},
				},
			},
		})
	}

	ip, _, err := net.SplitHostPort(sess.Conn.RemoteAddr().String())
	if err != nil {
		return err
	}
	svr.SendPacketMsg(sess.Conn, &msgsvr.InfosMessage{
		ChatId: enum.InfosMessageChatId.Info,
		Messages: []typ.InfosMessageMessage{
			{
				Id:   153,
				Args: []string{ip},
			},
		},
	})

	svr.SendPacketMsg(sess.Conn, &msgsvr.GameCreateSuccess{
		Type: enum.GameCreateType.Solo,
	})

	svr.SendPacketMsg(sess.Conn, &msgsvr.AccountStats{
		XP:               int(sess.Character.XP),
		XPLow:            sess.Character.XPLow(),
		XPHigh:           sess.Character.XPHigh(),
		Kama:             0,
		BonusPoints:      1,
		BonusPointsSpell: 1,
		Alignment:        0,
		FakeAlignment:    0,
		RankValue:        0,
		Honour:           0,
		Disgrace:         0,
		AlignmentEnabled: false,
		LP:               1000,
		LPMax:            1000,
		Energy:           10000,
		EnergyMax:        10000,
		Initiative:       321,
		Discernment:      123,
		AP: typ.AccountStatsStat{
			Base:      1,
			Equipment: 2,
			Feats:     3,
			Boost:     4,
		},
		MP: typ.AccountStatsStat{
			Base:      2,
			Equipment: 0,
			Feats:     0,
			Boost:     0,
		},
		Strength: typ.AccountStatsStat{
			Base:      3,
			Equipment: 0,
			Feats:     0,
			Boost:     0,
		},
		Vitality: typ.AccountStatsStat{
			Base:      4,
			Equipment: 0,
			Feats:     0,
			Boost:     0,
		},
		Wisdom: typ.AccountStatsStat{
			Base:      5,
			Equipment: 0,
			Feats:     0,
			Boost:     0,
		},
		Chance: typ.AccountStatsStat{
			Base:      6,
			Equipment: 0,
			Feats:     0,
			Boost:     0,
		},
		Agility: typ.AccountStatsStat{
			Base:      7,
			Equipment: 0,
			Feats:     0,
			Boost:     0,
		},
		Intelligence: typ.AccountStatsStat{
			Base:      8,
			Equipment: 0,
			Feats:     0,
			Boost:     0,
		},
		RangeModerator: typ.AccountStatsStat{
			Base:      9,
			Equipment: 0,
			Feats:     0,
			Boost:     0,
		},
		MaxSummonedCreatures: typ.AccountStatsStat{
			Base:      10,
			Equipment: 0,
			Feats:     0,
			Boost:     0,
		},
		BonusDamage: typ.AccountStatsStat{
			Base:      11,
			Equipment: 0,
			Feats:     0,
			Boost:     0,
		},
		PhysicalBonusDamage: typ.AccountStatsStat{
			Base:      12,
			Equipment: 0,
			Feats:     0,
			Boost:     0,
		},
		WeaponSkillBonus: typ.AccountStatsStat{
			Base:      13,
			Equipment: 0,
			Feats:     0,
			Boost:     0,
		},
		BonusDamagePercent: typ.AccountStatsStat{
			Base:      14,
			Equipment: 0,
			Feats:     0,
			Boost:     0,
		},
		HealBonus: typ.AccountStatsStat{
			Base:      15,
			Equipment: 0,
			Feats:     0,
			Boost:     0,
		},
		TrapBonus: typ.AccountStatsStat{
			Base:      16,
			Equipment: 0,
			Feats:     0,
			Boost:     0,
		},
		TrapBonusPercent: typ.AccountStatsStat{
			Base:      17,
			Equipment: 0,
			Feats:     0,
			Boost:     0,
		},
		DamageReflected: typ.AccountStatsStat{
			Base:      18,
			Equipment: 0,
			Feats:     0,
			Boost:     0,
		},
		CriticalHitBonus: typ.AccountStatsStat{
			Base:      19,
			Equipment: 0,
			Feats:     0,
			Boost:     0,
		},
		CriticalFailureBonus: typ.AccountStatsStat{
			Base:      20,
			Equipment: 0,
			Feats:     0,
			Boost:     0,
		},
		DodgeAP: typ.AccountStatsStat{
			Base:      21,
			Equipment: 0,
			Feats:     0,
			Boost:     0,
		},
		DodgeMP: typ.AccountStatsStat{
			Base:      22,
			Equipment: 0,
			Feats:     0,
			Boost:     0,
		},
		NeutralResistanceFixed: typ.AccountStatsStat{
			Base:      23,
			Equipment: 0,
			Feats:     0,
			Boost:     0,
		},
		NeutralResistancePercent: typ.AccountStatsStat{
			Base:      24,
			Equipment: 0,
			Feats:     0,
			Boost:     0,
		},
		NeutralResistanceFixedPVP: typ.AccountStatsStat{
			Base:      25,
			Equipment: 0,
			Feats:     0,
			Boost:     0,
		},
		NeutralResistancePercentPVP: typ.AccountStatsStat{
			Base:      26,
			Equipment: 0,
			Feats:     0,
			Boost:     0,
		},
		EarthResistanceFixed: typ.AccountStatsStat{
			Base:      27,
			Equipment: 0,
			Feats:     0,
			Boost:     0,
		},
		EarthResistancePercent: typ.AccountStatsStat{
			Base:      28,
			Equipment: 0,
			Feats:     0,
			Boost:     0,
		},
		EarthResistanceFixedPVP: typ.AccountStatsStat{
			Base:      29,
			Equipment: 0,
			Feats:     0,
			Boost:     0,
		},
		EarthResistancePercentPVP: typ.AccountStatsStat{
			Base:      30,
			Equipment: 0,
			Feats:     0,
			Boost:     0,
		},
		WaterResistanceFixed: typ.AccountStatsStat{
			Base:      31,
			Equipment: 0,
			Feats:     0,
			Boost:     0,
		},
		WaterResistancePercent: typ.AccountStatsStat{
			Base:      32,
			Equipment: 0,
			Feats:     0,
			Boost:     0,
		},
		WaterResistanceFixedPVP: typ.AccountStatsStat{
			Base:      33,
			Equipment: 0,
			Feats:     0,
			Boost:     0,
		},
		WaterResistancePercentPVP: typ.AccountStatsStat{
			Base:      34,
			Equipment: 0,
			Feats:     0,
			Boost:     0,
		},
		AirResistanceFixed: typ.AccountStatsStat{
			Base:      35,
			Equipment: 0,
			Feats:     0,
			Boost:     0,
		},
		AirResistancePercent: typ.AccountStatsStat{
			Base:      36,
			Equipment: 0,
			Feats:     0,
			Boost:     0,
		},
		AirResistanceFixedPVP: typ.AccountStatsStat{
			Base:      37,
			Equipment: 0,
			Feats:     0,
			Boost:     0,
		},
		AirResistancePercentPVP: typ.AccountStatsStat{
			Base:      38,
			Equipment: 0,
			Feats:     0,
			Boost:     0,
		},
		FireResistanceFixed: typ.AccountStatsStat{
			Base:      39,
			Equipment: 0,
			Feats:     0,
			Boost:     0,
		},
		FireResistancePercent: typ.AccountStatsStat{
			Base:      40,
			Equipment: 0,
			Feats:     0,
			Boost:     0,
		},
		FireResistanceFixedPVP: typ.AccountStatsStat{
			Base:      41,
			Equipment: 0,
			Feats:     0,
			Boost:     0,
		},
		FireResistancePercentPVP: typ.AccountStatsStat{
			Base:      42,
			Equipment: 0,
			Feats:     0,
			Boost:     0,
		},
	})

	svr.SendPacketMsg(sess.Conn, &msgsvr.InfosLifeRestoreTimerStart{Interval: time.Second * 2})

	// TODO: send GameMapData

	svr.SendPacketMsg(sess.Conn, &msgsvr.BasicsTime{Value: time.Now()})
	svr.SendPacketMsg(sess.Conn, &msgsvr.FightsCount{Value: 0})
	svr.SendPacketMsg(sess.Conn, &msgsvr.TutorialShowTip{Id: 32})

	return nil
}
