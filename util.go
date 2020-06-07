package d1game

import (
	"github.com/kralamoure/d1"
	"github.com/kralamoure/d1/d1typ"
	prototyp "github.com/kralamoure/d1proto/typ"
)

func protoAccessories(items map[int]d1.CharacterItem) prototyp.CommonAccessories {
	var weapon prototyp.CommonAccessoriesAccessory
	var hat prototyp.CommonAccessoriesAccessory
	var cloak prototyp.CommonAccessoriesAccessory
	var pet prototyp.CommonAccessoriesAccessory
	var shield prototyp.CommonAccessoriesAccessory

	for _, v := range items {
		if v.Position == d1typ.CharacterItemPositionInventory {
			continue
		}

		switch v.Position {
		case d1typ.CharacterItemPositionWeapon:
			weapon.TemplateId = v.TemplateId
		case d1typ.CharacterItemPositionHat:
			hat.TemplateId = v.TemplateId
		case d1typ.CharacterItemPositionCloak:
			cloak.TemplateId = v.TemplateId
		case d1typ.CharacterItemPositionPet:
			pet.TemplateId = v.TemplateId
		case d1typ.CharacterItemPositionShield:
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

func itemBatch(item d1.Item, others map[int]d1.Item) (d1.Item, bool) {
	for _, other := range others {
		if shouldJoinItems(item, other) {
			return other, true
		}
	}

	return d1.Item{}, false
}

func shouldJoinItems(item1 d1.Item, item2 d1.Item) bool {
	if sameItems(item1, item2) == false {
		return false
	}

	return true
}

func sameItems(item1 d1.Item, item2 d1.Item) bool {
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
