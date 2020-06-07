package d1game

import (
	"context"
	"errors"

	protoenum "github.com/kralamoure/d1proto/enum"
	"github.com/kralamoure/d1proto/msgcli"
	"github.com/kralamoure/d1proto/msgsvr"
	prototyp "github.com/kralamoure/d1proto/typ"
)

func (s *session) actionMovement(ctx context.Context, m msgcli.GameActionsSendActionsActionMovement) error {
	if len(m.DirAndCells) == 0 {
		return errInvalidRequest
	}

	char, err := s.svr.d1.Character(ctx, s.characterId)
	if err != nil {
		return err
	}

	gameMap, ok := s.svr.cache.static.gameMaps[char.GameMapId]
	if !ok {
		return errors.New("game map not found")
	}

	s.svr.mu.Lock()
	cells, ok := s.svr.cache.gameMapCells[gameMap.Id]
	s.svr.mu.Unlock()

	if !ok {
		cells, err = gameMap.Cells()
		if err != nil {
			return err
		}

		s.svr.mu.Lock()
		s.svr.cache.gameMapCells[gameMap.Id] = cells
		s.svr.mu.Unlock()
	}

	validated, err := validatedPath(m.DirAndCells, char.Cell, gameMap.Width, cells)
	if err != nil {
		s.svr.logger.Debugw("could not get validated path",
			"error", err,
			"client_address", s.conn.RemoteAddr().String(),
		)

		s.sendMessage(msgsvr.GameActions{
			ActionType: protoenum.GameActionType.Default,
		})

		return nil
	}

	if len(validated) == 0 {
		s.sendMessage(msgsvr.GameActions{
			ActionType: protoenum.GameActionType.Default,
		})
		return nil
	}

	gameAction := msgsvr.GameActions{
		ActionType: protoenum.GameActionType.Movement,
		ActionMovement: msgsvr.GameActionsActionMovement{
			Id:       0,
			SpriteId: char.Id,
			DirAndCells: append([]prototyp.CommonDirAndCell{{
				DirId:  0,
				CellId: char.Cell,
			}}, validated...),
		},
	}
	s.busy.Inc()
	s.gameActions[0] = gameAction

	err = s.svr.sendMsgToMap(ctx, char.GameMapId, gameAction)
	if err != nil {
		return err
	}

	return nil
}

// TODO
func (s *session) actionChallenge(ctx context.Context, m msgcli.GameActionsSendActionsActionChallenge) error {
	if s.busy.Load() > 0 {
		s.sendMessage(msgsvr.GameActions{
			ActionType: protoenum.GameActionType.ChallengeJoin,
			ActionChallengeJoin: msgsvr.GameActionsActionChallengeJoin{
				ChallengerId: s.characterId,
				ErrorReason:  protoenum.GameActionChallengeJoinErrorReason.YouAreBusy,
			},
		})
		return nil
	}

	char, err := s.svr.d1.Character(ctx, s.characterId)
	if err != nil {
		return err
	}

	otherChar, err := s.svr.d1.Character(ctx, m.ChallengedId)
	if err != nil {
		return err
	}

	// TODO
	if otherChar.GameMapId != char.GameMapId {
		return errInvalidRequest
	}

	s.svr.mu.Lock()
	otherSess, ok := s.svr.sessionByCharacterId[otherChar.Id]
	if !ok {
		s.svr.mu.Unlock()
		return err
	}
	s.svr.mu.Unlock()

	if otherSess.busy.Load() > 0 {
		s.sendMessage(msgsvr.GameActions{
			ActionType: protoenum.GameActionType.ChallengeJoin,
			ActionChallengeJoin: msgsvr.GameActionsActionChallengeJoin{
				ChallengerId: s.characterId,
				ErrorReason:  protoenum.GameActionChallengeJoinErrorReason.OpponentBusy,
			},
		})
		return nil
	}

	err = s.svr.sendMsgToMap(ctx, char.GameMapId, msgsvr.GameActions{
		ActionType: protoenum.GameActionType.Challenge,
		ActionChallenge: msgsvr.GameActionsActionChallenge{
			ChallengerId: char.Id,
			ChallengedId: m.ChallengedId,
		},
	})
	if err != nil {
		return err
	}

	return nil
}

// TODO
func (s *session) actionChallengeAccept(ctx context.Context, m msgcli.GameActionsSendActionsActionChallengeAccept) error {
	return errNotImplemented
}

// TODO
func (s *session) actionChallengeRefuse(ctx context.Context, m msgcli.GameActionsSendActionsActionChallengeRefuse) error {
	return errNotImplemented
}
