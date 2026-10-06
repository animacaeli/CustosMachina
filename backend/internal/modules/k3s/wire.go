package k3s

import "github.com/google/wire"

var Set = wire.NewSet(NewService, NewHandler)
