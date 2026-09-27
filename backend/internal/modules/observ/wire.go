// wire 装配集合。
package observ

import "github.com/google/wire"

var Set = wire.NewSet(NewService, NewHandler)
