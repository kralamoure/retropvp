package retropvp

import (
	"github.com/kralamoure/retro"
	"github.com/kralamoure/retro/retrotyp"
	prototyp "github.com/kralamoure/retroproto/typ"
)

func protoAccessories(items map[int]retro.CharacterItem) prototyp.CommonAccessories {
	var weapon prototyp.CommonAccessoriesAccessory
	var hat prototyp.CommonAccessoriesAccessory
	var cloak prototyp.CommonAccessoriesAccessory
	var pet prototyp.CommonAccessoriesAccessory
	var shield prototyp.CommonAccessoriesAccessory

	for _, v := range items {
		if v.Position == retrotyp.CharacterItemPositionInventory {
			continue
		}

		switch v.Position {
		case retrotyp.CharacterItemPositionWeapon:
			weapon.TemplateId = v.TemplateId
		case retrotyp.CharacterItemPositionHat:
			hat.TemplateId = v.TemplateId
		case retrotyp.CharacterItemPositionCloak:
			cloak.TemplateId = v.TemplateId
		case retrotyp.CharacterItemPositionPet:
			pet.TemplateId = v.TemplateId
		case retrotyp.CharacterItemPositionShield:
			shield.TemplateId = v.TemplateId
		}
	}

	return prototyp.CommonAccessories{
		Weapon: weapon,
		Hat:    hat,
		Cape:   cloak,
		Pet:    pet,
		Shield: shield,
	}
}

func itemBatch(item retro.Item, others map[int]retro.Item) (retro.Item, bool) {
	for _, other := range others {
		if shouldJoinItems(item, other) {
			return other, true
		}
	}

	return retro.Item{}, false
}

func shouldJoinItems(item1 retro.Item, item2 retro.Item) bool {
	if sameItems(item1, item2) == false {
		return false
	}

	return true
}

func sameItems(item1 retro.Item, item2 retro.Item) bool {
	if item1.TemplateId != item2.TemplateId {
		return false
	}

	if len(item1.Effects) != len(item2.Effects) {
		return false
	}

	for i, effect := range item1.Effects {
		if effect != item2.Effects[i] {
			return false
		}
	}

	return true
}
