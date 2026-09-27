// wire 装配集合：Service/Handler/Scheduler。
package cron

import "github.com/google/wire"

var Set = wire.NewSet(NewService, NewHandler, NewScheduler)
