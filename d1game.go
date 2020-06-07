package d1game

import (
	"errors"
	"math"
	"net"
	"time"

	"github.com/happybydefault/logger"
	"github.com/kralamoure/d1/d1svc"
	prototyp "github.com/kralamoure/d1proto/typ"
	"github.com/kralamoure/d1util"
	"github.com/kralamoure/dofus/dofussvc"
)

type Config struct {
	Id          int
	Addr        string
	ConnTimeout time.Duration
	TicketDur   time.Duration
	Location    *time.Location
	Dofus       *dofussvc.Service
	D1          *d1svc.Service
	Logger      logger.Logger
}

func NewServer(c Config) (*Server, error) {
	if c.Id <= 0 {
		return nil, errors.New("invalid id")
	}
	if c.ConnTimeout <= 0 {
		c.ConnTimeout = 30 * time.Minute
	}
	if c.TicketDur <= 0 {
		c.TicketDur = 20 * time.Second
	}
	if c.Location == nil {
		c.Location = time.UTC
	}
	if c.Dofus == nil {
		return nil, errors.New("nil dofus service")
	}
	if c.D1 == nil {
		return nil, errors.New("nil d1 service")
	}
	if c.Logger == nil {
		c.Logger = logger.Noop{}
	}

	addr, err := net.ResolveTCPAddr("tcp4", c.Addr)
	if err != nil {
		return nil, err
	}
	s := &Server{
		logger:               c.Logger,
		id:                   c.Id,
		addr:                 addr,
		connTimeout:          c.ConnTimeout,
		ticketDur:            c.TicketDur,
		location:             c.Location,
		dofus:                c.Dofus,
		d1:                   c.D1,
		sessions:             make(map[*session]struct{}),
		sessionByAccountId:   make(map[string]*session),
		sessionByCharacterId: make(map[int]*session),
	}
	return s, nil
}

func validatedPath(original []prototyp.CommonDirAndCell, startingCellId int, gameMapWidth int, cells []d1util.Cell) ([]prototyp.CommonDirAndCell, error) {
	if len(original) > 10 {
		return nil, errors.New("path is too long")
	}

	if len(cells) < startingCellId+1 {
		return nil, errors.New("starting cell not found")
	}
	current := cells[startingCellId]

	var dirAndCells []prototyp.CommonDirAndCell

	cellIds := make(map[int]struct{})
	cellIds[current.Id] = struct{}{}
	for _, v := range original {
		ix, err := d1util.DirectionToIndex(v.DirId)
		if err != nil {
			return nil, err
		}

		dirAndCell := prototyp.CommonDirAndCell{
			DirId:  v.DirId,
			CellId: -1,
		}

		for i := 0; i <= 100; i++ {
			if i == 100 {
				return nil, errors.New("path is too long")
			}

			nextId, ok := d1util.AroundCellNum(current.Id, 0, ix, gameMapWidth, cells)
			if !ok {
				return nil, errors.New("invalid next cell")
			}

			_, ok = cellIds[nextId]
			if ok {
				return nil, errors.New("repeated cell")
			}
			cellIds[nextId] = struct{}{}

			if len(cells) < nextId+1 {
				return nil, errors.New("next cell not found")
			}
			next := cells[nextId]

			if !next.Active || !next.LineOfSight || next.Movement <= 1 || math.Abs(float64(current.GroundLevel-next.GroundLevel)) > 1 {
				break
			}

			dirAndCell.CellId = nextId

			current = next

			if current.Id == v.CellId {
				break
			}
		}

		if dirAndCell.CellId == -1 {
			break
		}
		dirAndCells = append(dirAndCells, dirAndCell)
	}

	return dirAndCells, nil
}
