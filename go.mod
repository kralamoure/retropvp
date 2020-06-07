module github.com/kralamoure/d1game

go 1.16

require (
	github.com/PaesslerAG/gval v1.1.0
	github.com/antonmedv/expr v1.8.9
	github.com/go-ozzo/ozzo-validation/v4 v4.3.0
	github.com/happybydefault/logger v1.1.0
	github.com/jackc/pgx/v4 v4.11.0
	github.com/kralamoure/d1 v0.0.0-20210325215504-184ee80d8398
	github.com/kralamoure/d1pg v0.0.0-20210325220521-efbd35126a5c
	github.com/kralamoure/d1proto v0.0.0-20201009010506-b246d3a2e855
	github.com/kralamoure/d1util v0.0.0-20210427234418-7cd5ab0cb6e3
	github.com/kralamoure/dofus v0.0.0-20200927021741-893c10151570
	github.com/kralamoure/dofuspg v0.0.0-20200917030704-67fe21d1f864
	github.com/pkg/errors v0.9.1 // indirect
	github.com/spf13/pflag v1.0.5
	github.com/zippoxer/golang-petname v0.0.0-20190426180220-f3990f9184fc
	go.uber.org/atomic v1.7.0
	go.uber.org/multierr v1.6.0 // indirect
	go.uber.org/zap v1.16.0
	golang.org/x/lint v0.0.0-20200302205851-738671d3881b // indirect
	golang.org/x/time v0.0.0-20210220033141-f8bda1e9f3ba
	golang.org/x/tools v0.0.0-20200519015757-0d0afa43d58a // indirect
	honnef.co/go/tools v0.0.1-2020.1.4 // indirect
)

replace github.com/kralamoure/dofus => ../dofus

replace github.com/kralamoure/dofuspg => ../dofuspg

replace github.com/kralamoure/d1 => ../d1

replace github.com/kralamoure/d1pg => ../d1pg

replace github.com/kralamoure/d1proto => ../d1proto

replace github.com/kralamoure/d1util => ../d1util
