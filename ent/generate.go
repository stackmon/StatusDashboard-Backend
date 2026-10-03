package ent

import (
	"entgo.io/ent/entc"
)

//go:generate go run -mod=mod entgo.io/ent/cmd/ent generate --feature sql/upsert --feature sql/versioned-migration ./schema

var _ = entc.Generate
