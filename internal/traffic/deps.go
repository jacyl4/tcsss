package traffic

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"

	"github.com/vishvananda/netlink"
)

// NetlinkClient abstracts netlink operations for easier testing and substitution.
type NetlinkClient interface {
	LinkList() ([]netlink.Link, error)
	LinkByName(name string) (netlink.Link, error)
	LinkByIndex(index int) (netlink.Link, error)
	LinkDel(link netlink.Link) error
	LinkSetMTU(link netlink.Link, mtu int) error
	LinkSetTxQLen(link netlink.Link, qlen int) error
	RouteList(link netlink.Link, family int) ([]netlink.Route, error)
	RouteReplace(route *netlink.Route) error
	LinkSubscribeWithOptions(ch chan netlink.LinkUpdate, done chan struct{}, opts netlink.LinkSubscribeOptions) error
	AddrSubscribeWithOptions(ch chan netlink.AddrUpdate, done chan struct{}, opts netlink.AddrSubscribeOptions) error
}

// CommandExecutor abstracts command execution.
type CommandExecutor interface {
	Run(ctx context.Context, name string, args []string) (string, error)
}

type defaultNetlinkClient struct{}

func (defaultNetlinkClient) LinkList() ([]netlink.Link, error) {
	return netlink.LinkList()
}

func (defaultNetlinkClient) LinkByName(name string) (netlink.Link, error) {
	return netlink.LinkByName(name)
}

func (defaultNetlinkClient) LinkByIndex(index int) (netlink.Link, error) {
	return netlink.LinkByIndex(index)
}

func (defaultNetlinkClient) LinkDel(link netlink.Link) error {
	return netlink.LinkDel(link)
}

func (defaultNetlinkClient) LinkSetMTU(link netlink.Link, mtu int) error {
	return netlink.LinkSetMTU(link, mtu)
}

func (defaultNetlinkClient) LinkSetTxQLen(link netlink.Link, qlen int) error {
	return netlink.LinkSetTxQLen(link, qlen)
}

func (defaultNetlinkClient) RouteList(link netlink.Link, family int) ([]netlink.Route, error) {
	return netlink.RouteList(link, family)
}

func (defaultNetlinkClient) RouteReplace(route *netlink.Route) error {
	return netlink.RouteReplace(route)
}

func (defaultNetlinkClient) LinkSubscribeWithOptions(ch chan netlink.LinkUpdate, done chan struct{}, opts netlink.LinkSubscribeOptions) error {
	return netlink.LinkSubscribeWithOptions(ch, done, opts)
}

func (defaultNetlinkClient) AddrSubscribeWithOptions(ch chan netlink.AddrUpdate, done chan struct{}, opts netlink.AddrSubscribeOptions) error {
	return netlink.AddrSubscribeWithOptions(ch, done, opts)
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

func getLinkName(client NetlinkClient, index int) (string, error) {
	attrs, err := safeGetLinkAttrs(client, index)
	if err != nil {
		return "", err
	}
	if attrs.Name == "" {
		return "", fmt.Errorf("link name is empty for index %d", index)
	}
	return attrs.Name, nil
}
