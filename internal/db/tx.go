package db

import "gorm.io/gorm"

// Tx wraps the underlying ORM transaction. Callers pass it through to the
// *Tx facade methods and PublishTx; they never call methods on it directly.
type Tx struct {
	g *gorm.DB
}
