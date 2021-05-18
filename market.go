package retropvp

import (
	"context"
	"fmt"
	"sort"

	"github.com/kralamoure/retro"
	"github.com/kralamoure/retro/retrotyp"
	prototyp "github.com/kralamoure/retroproto/typ"
)

func (s *Server) marketTemplateIdsByItemType(ctx context.Context, market retro.Market, itemType retrotyp.ItemType) ([]int, error) {
	found := false
	for _, v := range market.Types {
		if v == itemType {
			found = true
			break
		}
	}
	if !found {
		return nil, errInvalidRequest
	}

	marketItems := s.cache.marketItemsByMarketId[market.Id]

	templateIds := make(map[int]struct{})
	for _, v := range marketItems {
		itemTemplate, ok := s.cache.static.items[v.TemplateId]
		if !ok {
			return nil, fmt.Errorf("invalid item template: %d", v.TemplateId)
		}
		if itemTemplate.Type != itemType {
			continue
		}
		templateIds[v.TemplateId] = struct{}{}
	}

	sli := make([]int, len(templateIds))
	i := 0
	for id := range templateIds {
		sli[i] = id
		i++
	}
	sort.Ints(sli)

	return sli, nil
}

func (s *Server) marketItemsByTemplateId(ctx context.Context, market retro.Market, templateId int) ([]prototyp.ExchangeBigStoreItemsListItem, error) {
	_, ok := s.cache.static.items[templateId]
	if !ok {
		return nil, errInvalidRequest
	}

	marketItems := s.cache.marketItemsByMarketId[market.Id]
	items := make(map[int]prototyp.ExchangeBigStoreItemsListItem)
	for k, v := range marketItems {
		if v.TemplateId != templateId {
			continue
		}

		effects := retro.EncodeItemEffects(v.Effects)

		items[k] = prototyp.ExchangeBigStoreItemsListItem{
			Id:        v.Id,
			Effects:   effects,
			PriceSet1: v.Price,
			PriceSet2: 0,
			PriceSet3: 0,
		}
	}

	sli := make([]prototyp.ExchangeBigStoreItemsListItem, len(items))
	i := 0
	for _, v := range items {
		sli[i] = v
		i++
	}
	sort.Slice(sli, func(i, j int) bool {
		return sli[i].Id < sli[j].Id
	})

	return sli, nil
}
