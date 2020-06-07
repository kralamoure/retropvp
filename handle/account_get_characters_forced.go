package handle

import (
	"github.com/kralamoure/d1proto/msgcli"

	"github.com/kralamoure/d1game"
)

func AccountGetCharactersForced(svr *d1game.Server, sess *d1game.Session, msg msgcli.AccountGetCharactersForced) error {
	return AccountGetCharacters(svr, sess, msgcli.AccountGetCharacters{})
}
