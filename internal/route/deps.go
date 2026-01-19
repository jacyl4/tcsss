package route

import (
	"time"

	"tcsss/internal/infra"
)

// Dependencies injects external services required by the optimizer.
type Dependencies struct {
	Netlink        infra.NetlinkClient
	Executor       infra.CommandExecutor
	CommandTimeout time.Duration
}
