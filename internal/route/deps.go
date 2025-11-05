package route

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/vishvananda/netlink"
)

// NetlinkClient abstracts netlink operations for easier testing.
type NetlinkClient interface {
	LinkList() ([]netlink.Link, error)
	LinkByName(name string) (netlink.Link, error)
	LinkByIndex(index int) (netlink.Link, error)
	LinkDel(link netlink.Link) error
	RouteList(link netlink.Link, family int) ([]netlink.Route, error)
	RouteReplace(route *netlink.Route) error
	LinkSubscribeWithOptions(ch chan netlink.LinkUpdate, done chan struct{}, opts netlink.LinkSubscribeOptions) error
	AddrSubscribeWithOptions(ch chan netlink.AddrUpdate, done chan struct{}, opts netlink.AddrSubscribeOptions) error
}

// CommandExecutor abstracts external command execution.
type CommandExecutor interface {
	Run(ctx context.Context, name string, args []string) (string, error)
}

// Dependencies injects external services required by the optimizer.
type Dependencies struct {
	Netlink        NetlinkClient
	Executor       CommandExecutor
	CommandTimeout time.Duration
}

type processExecutor struct{}

func (processExecutor) Run(ctx context.Context, name string, args []string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	return output.String(), err
}

func ensureExecutor(executor CommandExecutor) CommandExecutor {
	if executor != nil {
		return executor
	}
	return processExecutor{}
}

func safeGetLinkAttrs(client NetlinkClient, index int) (*netlink.LinkAttrs, error) {
	link, err := client.LinkByIndex(index)
	if err != nil {
		return nil, fmt.Errorf("link by index %d: %w", index, err)
	}
	if link == nil {
		return nil, fmt.Errorf("link is nil for index %d", index)
	}
	attrs := link.Attrs()
	if attrs == nil {
		return nil, fmt.Errorf("link attrs is nil for index %d", index)
	}
	return attrs, nil
}
