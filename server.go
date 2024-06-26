package retropvp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/happybydefault/logging"
	"github.com/kralamoure/dofus/dofussvc"
	"github.com/kralamoure/retro"
	"github.com/kralamoure/retro/retrosvc"
	"github.com/kralamoure/retro/retrotyp"
	"github.com/kralamoure/retroproto/msgsvr"
	prototyp "github.com/kralamoure/retroproto/typ"
	"github.com/kralamoure/retroutil"
)

type Server struct {
	logger      logging.Logger
	id          int
	addr        *net.TCPAddr
	connTimeout time.Duration
	ticketDur   time.Duration
	location    *time.Location
	dofus       *dofussvc.Service
	retro       *retrosvc.Service

	ln *net.TCPListener

	mu                   sync.Mutex
	sessions             map[*session]struct{}
	sessionByAccountId   map[string]*session
	sessionByCharacterId map[int]*session

	cache cache
}

type cache struct {
	static cacheStatic

	npcsByMapId           map[int][]retro.NPC
	markets               map[string]retro.Market
	marketItemsByMarketId map[string]map[int]retro.MarketItem
	gameMapCells          map[int][]retroutil.Cell
}

type cacheStatic struct {
	gameMaps     map[int]retro.GameMap
	effects      map[int]retro.EffectTemplate
	itemSets     map[int]retro.ItemSet
	items        map[int]retro.ItemTemplate
	npcs         map[int]retro.NPCTemplate
	npcDialogs   map[int]retro.NPCDialog
	npcResponses map[int]retro.NPCResponse
	classes      map[retrotyp.ClassId]retro.Class
	spells       map[int]retro.Spell
	mounts       map[int]retro.MountTemplate
}

func (s *Server) ListenAndServe(ctx context.Context) error {
	var wg sync.WaitGroup
	defer wg.Wait()

	gameServer, err := s.retro.GameServer(ctx, s.id)
	if err != nil {
		return err
	}
	s.id = gameServer.Id

	err = s.retro.SetGameServerState(ctx, retrotyp.GameServerStateOffline)
	if err != nil {
		return err
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		err := s.retro.SetGameServerState(ctx, retrotyp.GameServerStateOffline)
		if err != nil {
			s.logger.Error(fmt.Errorf("could not set game server state to offline: %w", err))
		}
		cancel()
	}()

	err = s.loadCache(ctx)
	if err != nil {
		return err
	}

	ln, err := net.ListenTCP("tcp4", s.addr)
	if err != nil {
		return err
	}
	defer func() {
		ln.Close()
		s.logger.Infow("stopped listening",
			"address", ln.Addr().String(),
		)
	}()
	s.logger.Infow("listening",
		"address", ln.Addr().String(),
	)
	s.ln = ln

	err = s.retro.SetGameServerState(ctx, retrotyp.GameServerStateOnline)
	if err != nil {
		return err
	}

	errCh := make(chan error)

	wg.Add(1)
	go func() {
		defer wg.Done()
		err := s.acceptLoop(ctx)
		if err != nil {
			select {
			case errCh <- err:
			case <-ctx.Done():
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		err := s.maintain(ctx)
		if err != nil {
			select {
			case errCh <- err:
			case <-ctx.Done():
			}
		}
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

func (s *Server) controlAccount(accountId string, sess *session) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	currentSess, ok := s.sessionByAccountId[accountId]
	if ok {
		currentSess.conn.Close()
		return errors.New("account already logged in")
	}

	sess.accountId = accountId
	s.sessionByAccountId[accountId] = sess

	return nil
}

func (s *Server) maintain(ctx context.Context) error {
	var wg sync.WaitGroup
	defer wg.Wait()

	t := time.NewTicker(6 * time.Hour)
	defer t.Stop()

	for {
		select {
		case <-t.C:
			err := s.deleteInvalidMounts(ctx)
			if err != nil {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (s *Server) deleteInvalidMounts(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	mounts, err := s.retro.Mounts(ctx)
	if err != nil {
		return err
	}

	for _, v := range mounts {
		if !v.Validity.IsZero() && v.Validity.Before(time.Now()) {
			err := s.retro.DeleteMount(ctx, v.Id)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (s *Server) acceptLoop(ctx context.Context) error {
	var wg sync.WaitGroup
	defer wg.Wait()

	for {
		conn, err := s.ln.AcceptTCP()
		if err != nil {
			return err
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			err := s.handleClientConn(ctx, conn)
			if err != nil {
				if errors.Is(err, os.ErrDeadlineExceeded) ||
					errors.Is(err, io.EOF) ||
					errors.Is(err, context.Canceled) ||
					errors.Is(err, errInvalidRequest) {
					s.logger.Debugw(fmt.Errorf("error while handling client connection: %w", err).Error(),
						"client_address", conn.RemoteAddr().String(),
					)
				} else {
					s.logger.Errorw(fmt.Errorf("error while handling client connection: %w", err).Error(),
						"client_address", conn.RemoteAddr().String(),
					)
				}
			}
		}()
	}
}

func (s *Server) handleClientConn(ctx context.Context, conn *net.TCPConn) error {
	sess := &session{
		svr:         s,
		conn:        conn,
		gameActions: make(map[int]msgsvr.GameActions),
	}

	s.trackSession(sess, true)
	defer s.trackSession(sess, false)

	var wg sync.WaitGroup
	defer wg.Wait()

	defer func() {
		conn.Close()
		s.logger.Infow("client disconnected",
			"client_address", conn.RemoteAddr().String(),
		)
	}()
	s.logger.Infow("client connected",
		"client_address", conn.RemoteAddr().String(),
	)

	err := conn.SetKeepAlivePeriod(1 * time.Minute)
	if err != nil {
		return err
	}
	err = conn.SetKeepAlive(true)
	if err != nil {
		return err
	}

	if s.connTimeout > 0 {
		err = conn.SetReadDeadline(time.Now().UTC().Add(s.connTimeout))
		if err != nil {
			return err
		}
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error)

	wg.Add(1)
	go func() {
		defer wg.Done()
		err := sess.receivePackets(ctx)
		if err != nil {
			select {
			case errCh <- err:
			case <-ctx.Done():
			}
		}
	}()

	sess.sendMessage(msgsvr.AksHelloGame{})

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		sess.sendMessage(msgsvr.AksServerMessage{Value: "04"})

		return ctx.Err()
	}
}

func (s *Server) loadCache(ctx context.Context) error {
	s.cache.gameMapCells = make(map[int][]retroutil.Cell)

	gameMaps, err := s.retro.GameMaps(ctx)
	if err != nil {
		return err
	}
	s.cache.static.gameMaps = gameMaps

	effectTemplates, err := s.retro.EffectTemplates(ctx)
	if err != nil {
		return err
	}
	s.cache.static.effects = effectTemplates

	itemSetTemplates, err := s.retro.ItemSets(ctx)
	if err != nil {
		return err
	}
	s.cache.static.itemSets = itemSetTemplates

	itemTemplates, err := s.retro.ItemTemplates(ctx)
	if err != nil {
		return err
	}
	s.cache.static.items = itemTemplates

	npcTemplates, err := s.retro.NPCTemplates(ctx)
	if err != nil {
		return err
	}
	s.cache.static.npcs = npcTemplates

	npcDialogs, err := s.retro.NPCDialogs(ctx)
	if err != nil {
		return err
	}
	s.cache.static.npcDialogs = npcDialogs

	npcResponse, err := s.retro.NPCResponses(ctx)
	if err != nil {
		return err
	}
	s.cache.static.npcResponses = npcResponse

	markets, err := s.retro.Markets(ctx)
	if err != nil {
		return err
	}
	s.cache.markets = markets

	s.cache.marketItemsByMarketId = make(map[string]map[int]retro.MarketItem, len(s.cache.markets))
	for id := range markets {
		marketItems, err := s.retro.MarketItemsByMarketId(ctx, id)
		if err != nil {
			return err
		}
		s.cache.marketItemsByMarketId[id] = marketItems
	}

	npcs, err := s.retro.NPCs(ctx)
	if err != nil {
		return err
	}

	s.cache.npcsByMapId = make(map[int][]retro.NPC)
	for _, v := range npcs {
		if s.cache.npcsByMapId[v.MapId] == nil {
			s.cache.npcsByMapId[v.MapId] = []retro.NPC{v}
		} else {
			s.cache.npcsByMapId[v.MapId] = append(s.cache.npcsByMapId[v.MapId], v)
		}
	}

	classes, err := s.retro.Classes(ctx)
	if err != nil {
		return err
	}
	s.cache.static.classes = classes

	spells, err := s.retro.Spells(ctx)
	if err != nil {
		return err
	}
	s.cache.static.spells = spells

	mountTemplates, err := s.retro.MountTemplates(ctx)
	if err != nil {
		return err
	}
	s.cache.static.mounts = mountTemplates

	return nil
}

func (s *Server) trackSession(sess *session, add bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if add {
		s.sessions[sess] = struct{}{}
	} else {
		delete(s.sessionByCharacterId, sess.characterId)
		delete(s.sessionByAccountId, sess.accountId)
		delete(s.sessions, sess)
	}
}

func (s *Server) gameMovementSpriteCharacter(ctx context.Context, char retro.Character, transition bool) (sprite msgsvr.GameMovementSprite, err error) {
	gfxId, err := strconv.Atoi(fmt.Sprintf("%d%d", char.ClassId, char.Sex))
	if err != nil {
		return
	}

	items, err := s.retro.CharacterItemsByCharacterId(ctx, char.Id)
	if err != nil {
		return
	}

	aura := 0
	level := char.Level()
	if level >= 200 {
		aura = 2
	} else if level >= 100 {
		aura = 1
	}

	mountTemplateId := 0
	mountCustomColor1 := ""
	mountCustomColor2 := ""
	mountCustomColor3 := ""
	if char.Mounting {
		var mount retro.Mount
		mount, err = s.retro.Mount(ctx, char.MountId)
		if err != nil {
			return
		}

		mountTemplateId = mount.TemplateId

		chameleon := false
		for _, v := range mount.Capacities {
			if v == retrotyp.MountCapacityIdChameleon {
				chameleon = true
				break
			}
		}

		if chameleon {
			mountCustomColor1 = string(char.Color2)
			mountCustomColor2 = string(char.Color3)
			mountCustomColor3 = string(char.Color3)
		}
	}

	sprite = msgsvr.GameMovementSprite{
		Transition: transition,
		Fight:      false,
		Type:       int(char.ClassId),
		Id:         char.Id,
		CellId:     char.Cell,
		Direction:  char.Direction,
		Character: msgsvr.GameMovementCharacter{
			Name:                      string(char.Name),
			Title:                     prototyp.CommonTitle{},
			AllowGhostMode:            false,
			GFXId:                     gfxId,
			Sex:                       0,
			ScaleX:                    100,
			ScaleY:                    100,
			Level:                     char.Level(),
			Color1:                    string(char.Color1),
			Color2:                    string(char.Color2),
			Color3:                    string(char.Color3),
			Accessories:               protoAccessories(items),
			AlignmentId:               int(char.Alignment),
			AlignmentLevel:            0,
			Grade:                     int(char.Grade()),
			AlignmentFallenAngelDemon: char.Disgrace > 0,
			Aura:                      aura,
			Emote:                     0,
			EmoteTimer:                0,
			GuildName:                 "",
			GuildEmblem:               prototyp.CommonGuildEmblem{},
			Restrictions:              msgsvr.AccountRestrictions{},
			MountModelId:              mountTemplateId,
			MountCustomColor1:         mountCustomColor1,
			MountCustomColor2:         mountCustomColor2,
			MountCustomColor3:         mountCustomColor3,
			LP:                        0,
			AP:                        0,
			MP:                        0,
			Resistances:               prototyp.CommonResistances{},
			Team:                      0,
			LinkedSprites:             msgsvr.GameMovementLinkedSprites{},
		},
	}

	return
}

func (s *Server) sendMsgToMap(ctx context.Context, id int, msg msgOut) error {
	var wg sync.WaitGroup
	defer wg.Wait()

	s.mu.Lock()
	defer s.mu.Unlock()

	chars, err := s.retro.CharactersByGameMapId(ctx, id)
	if err != nil {
		return err
	}

	for charId := range chars {
		sess, ok := s.sessionByCharacterId[charId]
		if !ok {
			continue
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			sess.sendMessage(msg)
		}()
	}

	return nil
}

func (s *Server) commonMountData(mount retro.Mount) (data prototyp.CommonMountData, err error) {
	mountTemplate, ok := s.cache.static.mounts[mount.TemplateId]
	if !ok {
		err = errors.New("mout template not found")
		return
	}

	level := mount.Level()

	data = prototyp.CommonMountData{
		Id:               mount.Id,
		ModelId:          mount.TemplateId,
		Ancestors:        [14]int{},
		Capacities:       mount.Capacities,
		Name:             mount.Name,
		Sex:              mount.Sex,
		XP:               mount.XP,
		XPMin:            mount.XPLow(),
		XPMax:            mount.XPHigh(),
		Level:            level,
		Mountable:        true,
		PodsMax:          0,
		Wild:             false,
		Stamina:          10000,
		StaminaMax:       10000,
		Maturity:         8000,
		MaturityMax:      8000,
		Energy:           10000,
		EnergyMax:        10000,
		Serenity:         0,
		SerenityMin:      -10000,
		SerenityMax:      10000,
		Love:             10000,
		LoveMax:          10000,
		Fecundation:      0,
		Fecundable:       true,
		Effects:          mountTemplate.Effects(level),
		Tiredness:        0,
		TirednessMax:     240,
		Reproductions:    0,
		ReproductionsMax: 20,
	}

	return
}
